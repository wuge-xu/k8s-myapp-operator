/*
Copyright 2026.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	appv1 "k8s-myapp-operator/api/v1"
)

const myAppFinalizer = "cleanup.app.demo.io"

// 定义三个 Prometheus 指标
// Counter：只增不减的计数器，适合记录"发生了多少次"
// Histogram：分布直方图，适合记录"耗时分布"
var (
	reconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myapp_reconcile_total",
			Help: "Total number of reconciliations",
		},
		[]string{"name", "namespace"},
	)

	reconcileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myapp_reconcile_errors_total",
			Help: "Total number of reconciliation errors",
		},
		[]string{"name", "namespace"},
	)

	reconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myapp_reconcile_duration_seconds",
			Help:    "Duration of reconciliation in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"name", "namespace"},
	)
)

func init() {
	// 把这三个指标注册到 controller-runtime 的默认注册表
	// controller-runtime 的 /metrics 端点会自动暴露这个注册表里的所有指标
	metrics.Registry.MustRegister(reconcileTotal, reconcileErrors, reconcileDuration)
}

type MyAppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=app.demo.io,resources=myapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=app.demo.io,resources=myapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=app.demo.io,resources=myapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete

func (r *MyAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	start := time.Now()

	var myApp appv1.MyApp
	if err := r.Get(ctx, req.NamespacedName, &myApp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 记录调谐次数
	reconcileTotal.WithLabelValues(myApp.Name, myApp.Namespace).Inc()

	var reconcileErr error
	defer func() {
		// 记录调谐耗时（无论成功还是失败都记录）
		reconcileDuration.WithLabelValues(myApp.Name, myApp.Namespace).
			Observe(time.Since(start).Seconds())
		// 如果有错误，记录错误次数
		if reconcileErr != nil {
			reconcileErrors.WithLabelValues(myApp.Name, myApp.Namespace).Inc()
		}
	}()

	if !myApp.DeletionTimestamp.IsZero() {
		var result ctrl.Result
		result, reconcileErr = r.handleDeletion(ctx, &myApp, log)
		return result, reconcileErr
	}

	if !controllerutil.ContainsFinalizer(&myApp, myAppFinalizer) {
		log.Info("adding finalizer", "finalizer", myAppFinalizer)
		controllerutil.AddFinalizer(&myApp, myAppFinalizer)
		if reconcileErr = r.Update(ctx, &myApp); reconcileErr != nil {
			return ctrl.Result{}, reconcileErr
		}
	}

	if reconcileErr = r.reconcileConfigMap(ctx, &myApp, log); reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}

	if reconcileErr = r.reconcileDeployment(ctx, &myApp, log); reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}

	if reconcileErr = r.reconcileService(ctx, &myApp, log); reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}

	if reconcileErr = r.reconcileHPA(ctx, &myApp, log); reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}

	var existingDeployment appsv1.Deployment
	if reconcileErr = r.Get(ctx, types.NamespacedName{
		Name:      myApp.Name,
		Namespace: myApp.Namespace,
	}, &existingDeployment); reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}

	readyReplicas := existingDeployment.Status.ReadyReplicas
	phase := "Pending"
	if readyReplicas == myApp.Spec.Replicas {
		phase = "Running"
	} else if readyReplicas > 0 {
		phase = "Degraded"
	}

	var result ctrl.Result
	result, reconcileErr = r.updateStatus(ctx, &myApp, phase, readyReplicas)
	return result, reconcileErr
}

func (r *MyAppReconciler) handleDeletion(ctx context.Context, myApp *appv1.MyApp, log logr.Logger) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(myApp, myAppFinalizer) {
		return ctrl.Result{}, nil
	}
	log.Info("executing cleanup before deletion", "name", myApp.Name)
	controllerutil.RemoveFinalizer(myApp, myAppFinalizer)
	if err := r.Update(ctx, myApp); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("finalizer removed, object will be deleted", "name", myApp.Name)
	return ctrl.Result{}, nil
}

func (r *MyAppReconciler) reconcileHPA(ctx context.Context, myApp *appv1.MyApp, log logr.Logger) error {
	if myApp.Spec.Autoscaling == nil || !myApp.Spec.Autoscaling.Enabled {
		return nil
	}

	desired := buildHPA(myApp, r.Scheme)

	var existing autoscalingv2.HorizontalPodAutoscaler
	err := r.Get(ctx, types.NamespacedName{
		Name:      myApp.Name + "-hpa",
		Namespace: myApp.Namespace,
	}, &existing)

	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating hpa", "name", desired.Name)
			return r.Create(ctx, desired)
		}
		return err
	}

	needsUpdate := false
	if myApp.Spec.Autoscaling.MinReplicas != nil &&
		(existing.Spec.MinReplicas == nil ||
			*existing.Spec.MinReplicas != *myApp.Spec.Autoscaling.MinReplicas) {
		existing.Spec.MinReplicas = myApp.Spec.Autoscaling.MinReplicas
		needsUpdate = true
	}
	if myApp.Spec.Autoscaling.MaxReplicas != nil &&
		existing.Spec.MaxReplicas != *myApp.Spec.Autoscaling.MaxReplicas {
		existing.Spec.MaxReplicas = *myApp.Spec.Autoscaling.MaxReplicas
		needsUpdate = true
	}
	if needsUpdate {
		log.Info("updating hpa", "name", existing.Name)
		return r.Update(ctx, &existing)
	}

	return nil
}

func (r *MyAppReconciler) reconcileConfigMap(ctx context.Context, myApp *appv1.MyApp, log logr.Logger) error {
	if len(myApp.Spec.Config) == 0 {
		return nil
	}

	desired := buildConfigMap(myApp, r.Scheme)

	var existing corev1.ConfigMap
	err := r.Get(ctx, types.NamespacedName{
		Name:      myApp.Name + "-config",
		Namespace: myApp.Namespace,
	}, &existing)

	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating configmap", "name", desired.Name)
			return r.Create(ctx, desired)
		}
		return err
	}

	if !reflect.DeepEqual(existing.Data, myApp.Spec.Config) {
		log.Info("updating configmap", "name", existing.Name)
		existing.Data = myApp.Spec.Config
		return r.Update(ctx, &existing)
	}

	return nil
}

func (r *MyAppReconciler) reconcileDeployment(ctx context.Context, myApp *appv1.MyApp, log logr.Logger) error {
	desired := buildDeployment(myApp, r.Scheme)

	var existing appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{
		Name:      myApp.Name,
		Namespace: myApp.Namespace,
	}, &existing)

	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating deployment", "name", desired.Name)
			return r.Create(ctx, desired)
		}
		return err
	}

	currentImage := ""
	if len(existing.Spec.Template.Spec.Containers) > 0 {
		currentImage = existing.Spec.Template.Spec.Containers[0].Image
	}

	desiredImage := myApp.Spec.Image
	if desiredImage == "" {
		desiredImage = "nginx:latest"
	}

	needsUpdate := false
	if *existing.Spec.Replicas != myApp.Spec.Replicas {
		existing.Spec.Replicas = &myApp.Spec.Replicas
		needsUpdate = true
	}
	if currentImage != desiredImage {
		log.Info("updating image", "from", currentImage, "to", desiredImage)
		existing.Spec.Template.Spec.Containers[0].Image = desiredImage
		needsUpdate = true
	}
	if needsUpdate {
		return r.Update(ctx, &existing)
	}

	return nil
}

func (r *MyAppReconciler) reconcileService(ctx context.Context, myApp *appv1.MyApp, log logr.Logger) error {
	desired := buildService(myApp, r.Scheme)

	var existing corev1.Service
	err := r.Get(ctx, types.NamespacedName{
		Name:      myApp.Name + "-service",
		Namespace: myApp.Namespace,
	}, &existing)

	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating service", "name", desired.Name)
			return r.Create(ctx, desired)
		}
		return err
	}

	if len(existing.Spec.Ports) > 0 &&
		existing.Spec.Ports[0].Port != myApp.Spec.Port {
		existing.Spec.Ports[0].Port = myApp.Spec.Port
		existing.Spec.Ports[0].TargetPort = intstr.FromInt32(myApp.Spec.Port)
		return r.Update(ctx, &existing)
	}

	return nil
}

func (r *MyAppReconciler) updateStatus(ctx context.Context, myApp *appv1.MyApp, phase string, readyReplicas int32) (ctrl.Result, error) {
	myApp.Status.Phase = phase
	myApp.Status.ReadyReplicas = readyReplicas
	if err := r.Status().Update(ctx, myApp); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func buildHPA(myApp *appv1.MyApp, scheme *runtime.Scheme) *autoscalingv2.HorizontalPodAutoscaler {
	as := myApp.Spec.Autoscaling
	cpuTarget := int32(80)
	if as.TargetCPUUtilizationPercentage != nil {
		cpuTarget = *as.TargetCPUUtilizationPercentage
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myApp.Name + "-hpa",
			Namespace: myApp.Namespace,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       myApp.Name,
			},
			MinReplicas: as.MinReplicas,
			MaxReplicas: *as.MaxReplicas,
			Metrics: []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: corev1.ResourceCPU,
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.UtilizationMetricType,
							AverageUtilization: &cpuTarget,
						},
					},
				},
			},
		},
	}

	controllerutil.SetControllerReference(myApp, hpa, scheme)
	return hpa
}

func buildConfigMap(myApp *appv1.MyApp, scheme *runtime.Scheme) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myApp.Name + "-config",
			Namespace: myApp.Namespace,
		},
		Data: myApp.Spec.Config,
	}
	controllerutil.SetControllerReference(myApp, cm, scheme)
	return cm
}

func buildDeployment(myApp *appv1.MyApp, scheme *runtime.Scheme) *appsv1.Deployment {
	replicas := myApp.Spec.Replicas
	labels := map[string]string{"app": myApp.Name}

	image := myApp.Spec.Image
	if image == "" {
		image = "nginx:latest"
	}

	var envFrom []corev1.EnvFromSource
	if len(myApp.Spec.Config) > 0 {
		envFrom = []corev1.EnvFromSource{
			{
				ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: myApp.Name + "-config",
					},
				},
			},
		}
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myApp.Name,
			Namespace: myApp.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "app",
							Image:   image,
							Ports:   []corev1.ContainerPort{{ContainerPort: myApp.Spec.Port}},
							EnvFrom: envFrom,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
						},
					},
				},
			},
		},
	}

	controllerutil.SetControllerReference(myApp, deployment, scheme)
	return deployment
}

func buildService(myApp *appv1.MyApp, scheme *runtime.Scheme) *corev1.Service {
	labels := map[string]string{"app": myApp.Name}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myApp.Name + "-service",
			Namespace: myApp.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Port:       myApp.Spec.Port,
					TargetPort: intstr.FromInt32(myApp.Spec.Port),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	controllerutil.SetControllerReference(myApp, svc, scheme)
	return svc
}

func (r *MyAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appv1.MyApp{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Named("myapp").
		Complete(r)
}
