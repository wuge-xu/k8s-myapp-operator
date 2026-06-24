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

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appv1 "k8s-myapp-operator/api/v1"
)

// myAppFinalizer 是我们给这个 Operator 起的 Finalizer 名字
// 命名惯例是 "操作.域名/版本"，全局唯一即可
const myAppFinalizer = "cleanup.app.demo.io"

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

func (r *MyAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var myApp appv1.MyApp
	if err := r.Get(ctx, req.NamespacedName, &myApp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 关键判断：检查 DeletionTimestamp 是否被设置
	// 用户执行 kubectl delete 之后，K8s 不会立刻删除对象，
	// 而是在对象上打上 DeletionTimestamp 这个时间戳，表示"待删除"
	// 控制器检测到这个时间戳，就知道该执行清理逻辑了
	if !myApp.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &myApp, log)
	}

	// 正常调谐流程：确保 Finalizer 已经注册到这个对象上
	// controllerutil.ContainsFinalizer 检查对象上是否已经有这个 Finalizer
	if !controllerutil.ContainsFinalizer(&myApp, myAppFinalizer) {
		log.Info("adding finalizer", "finalizer", myAppFinalizer)
		controllerutil.AddFinalizer(&myApp, myAppFinalizer)
		// 注意：修改 Finalizer 列表之后必须立刻 Update，
		// 否则这个变化只存在于内存里，不会写入 K8s
		if err := r.Update(ctx, &myApp); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.reconcileConfigMap(ctx, &myApp, log); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileDeployment(ctx, &myApp, log); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileService(ctx, &myApp, log); err != nil {
		return ctrl.Result{}, err
	}

	var existingDeployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{
		Name:      myApp.Name,
		Namespace: myApp.Namespace,
	}, &existingDeployment); err != nil {
		return ctrl.Result{}, err
	}

	readyReplicas := existingDeployment.Status.ReadyReplicas
	phase := "Pending"
	if readyReplicas == myApp.Spec.Replicas {
		phase = "Running"
	} else if readyReplicas > 0 {
		phase = "Degraded"
	}

	return r.updateStatus(ctx, &myApp, phase, readyReplicas)
}

// handleDeletion 在对象被标记为待删除时执行
func (r *MyAppReconciler) handleDeletion(ctx context.Context, myApp *appv1.MyApp, log logr.Logger) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(myApp, myAppFinalizer) {
		// Finalizer 已经被移除了，不需要再做任何事
		return ctrl.Result{}, nil
	}

	// 在这里执行你的自定义清理逻辑
	// 真实项目里可以是：通知外部系统、清理数据库记录、等待任务完成等
	// 这里我们用打印日志来模拟
	log.Info("executing cleanup before deletion",
		"name", myApp.Name,
		"namespace", myApp.Namespace,
	)

	// 清理完成，移除 Finalizer，K8s 才会真正删除这个对象
	controllerutil.RemoveFinalizer(myApp, myAppFinalizer)
	if err := r.Update(ctx, myApp); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("finalizer removed, object will be deleted", "name", myApp.Name)
	return ctrl.Result{}, nil
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

	if *existing.Spec.Replicas != myApp.Spec.Replicas {
		log.Info("updating deployment replicas", "name", existing.Name)
		existing.Spec.Replicas = &myApp.Spec.Replicas
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
		log.Info("updating service port", "name", existing.Name)
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
							Name:  "app",
							Image: "nginx:latest",
							Ports: []corev1.ContainerPort{
								{ContainerPort: myApp.Spec.Port},
							},
							EnvFrom: envFrom,
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
		Named("myapp").
		Complete(r)
}
