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
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	appv1 "k8s-myapp-operator/api/v1"
)

const (
	myAppFinalizer = "cleanup.app.demo.io"

	conditionTypeReady       = "Ready"
	conditionTypeProgressing = "Progressing"
	conditionTypeDegraded    = "Degraded"
)

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
	metrics.Registry.MustRegister(reconcileTotal, reconcileErrors, reconcileDuration)
}

type MyAppReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=app.demo.io,resources=myapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=app.demo.io,resources=myapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=app.demo.io,resources=myapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

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

	reconcileTotal.WithLabelValues(myApp.Name, myApp.Namespace).Inc()

	var reconcileErr error
	defer func() {
		reconcileDuration.WithLabelValues(myApp.Name, myApp.Namespace).
			Observe(time.Since(start).Seconds())
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
		return r.failReconcile(
			ctx,
			&myApp,
			"ConfigMapReconcileFailed",
			reconcileErr,
		)
	}

	if reconcileErr = r.reconcileDeployment(ctx, &myApp, log); reconcileErr != nil {
		return r.failReconcile(
			ctx,
			&myApp,
			"DeploymentReconcileFailed",
			reconcileErr,
		)
	}

	if reconcileErr = r.reconcileService(ctx, &myApp, log); reconcileErr != nil {
		return r.failReconcile(
			ctx,
			&myApp,
			"ServiceReconcileFailed",
			reconcileErr,
		)
	}

	if reconcileErr = r.reconcileHPA(ctx, &myApp, log); reconcileErr != nil {
		return r.failReconcile(
			ctx,
			&myApp,
			"HPAReconcileFailed",
			reconcileErr,
		)
	}

	var existingDeployment appsv1.Deployment
	if reconcileErr = r.Get(ctx, types.NamespacedName{
		Name:      myApp.Name,
		Namespace: myApp.Namespace,
	}, &existingDeployment); reconcileErr != nil {
		return r.failReconcile(
			ctx,
			&myApp,
			"DeploymentStatusReadFailed",
			reconcileErr,
		)
	}

	desiredReplicas := int32(1)
	if existingDeployment.Spec.Replicas != nil {
		desiredReplicas = *existingDeployment.Spec.Replicas
	}

	readyReplicas := existingDeployment.Status.ReadyReplicas

	deploymentReady :=
		existingDeployment.Status.ObservedGeneration >= existingDeployment.Generation &&
			existingDeployment.Status.ReadyReplicas == desiredReplicas &&
			existingDeployment.Status.UpdatedReplicas == desiredReplicas &&
			existingDeployment.Status.AvailableReplicas == desiredReplicas

	phase := "Progressing"

	if deploymentReady {
		phase = "Running"

		becameReady := r.setCondition(
			&myApp,
			conditionTypeReady,
			metav1.ConditionTrue,
			"AllReplicasReady",
			fmt.Sprintf(
				"All %d desired replicas are ready",
				desiredReplicas,
			),
		)

		r.setCondition(
			&myApp,
			conditionTypeProgressing,
			metav1.ConditionFalse,
			"ReconcileComplete",
			"Application reconciliation completed",
		)

		r.setCondition(
			&myApp,
			conditionTypeDegraded,
			metav1.ConditionFalse,
			"ResourcesHealthy",
			"All managed resources are healthy",
		)

		if becameReady {
			r.recordEventf(
				&myApp,
				corev1.EventTypeNormal,
				"Ready",
				"MyApp is ready with %d/%d replicas",
				readyReplicas,
				desiredReplicas,
			)
		}
	} else {
		message := fmt.Sprintf(
			"Waiting for replicas to become ready: %d/%d ready",
			readyReplicas,
			desiredReplicas,
		)

		r.setCondition(
			&myApp,
			conditionTypeReady,
			metav1.ConditionFalse,
			"ReplicasNotReady",
			message,
		)

		r.setCondition(
			&myApp,
			conditionTypeProgressing,
			metav1.ConditionTrue,
			"WaitingForReplicas",
			message,
		)

		r.setCondition(
			&myApp,
			conditionTypeDegraded,
			metav1.ConditionFalse,
			"ReconcileInProgress",
			"Application resources are still progressing",
		)
	}

	if reconcileErr = r.updateStatus(ctx, &myApp, phase, readyReplicas); reconcileErr != nil {
		return ctrl.Result{}, reconcileErr
	}

	if !deploymentReady {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// setCondition creates or updates one standard Kubernetes Condition.
func (r *MyAppReconciler) setCondition(
	myApp *appv1.MyApp,
	conditionType string,
	status metav1.ConditionStatus,
	reason string,
	message string,
) bool {
	return meta.SetStatusCondition(
		&myApp.Status.Conditions,
		metav1.Condition{
			Type:               conditionType,
			Status:             status,
			ObservedGeneration: myApp.Generation,
			Reason:             reason,
			Message:            message,
		},
	)
}

// failReconcile records a failed reconciliation in Status and Events and
// applies an explicit retry policy. Returning nil error for classified errors
// prevents controller-runtime from replacing our RequeueAfter with its own
// rate-limited error retry.
func (r *MyAppReconciler) failReconcile(
	ctx context.Context,
	myApp *appv1.MyApp,
	reason string,
	reconcileErr error,
) (ctrl.Result, error) {
	decision := classifyReconcileError(reconcileErr)
	message := fmt.Sprintf(
		"%s: %v (class=%s, policyReason=%s)",
		reason,
		reconcileErr,
		decision.Class,
		decision.Reason,
	)

	r.setCondition(
		myApp,
		conditionTypeReady,
		metav1.ConditionFalse,
		reason,
		message,
	)

	r.setCondition(
		myApp,
		conditionTypeProgressing,
		metav1.ConditionFalse,
		reason,
		"Reconciliation stopped because an error occurred",
	)

	r.setCondition(
		myApp,
		conditionTypeDegraded,
		metav1.ConditionTrue,
		reason,
		message,
	)

	if statusErr := r.updateStatus(
		ctx,
		myApp,
		"Degraded",
		myApp.Status.ReadyReplicas,
	); statusErr != nil {
		return ctrl.Result{}, fmt.Errorf(
			"%s; failed to persist degraded status: %w",
			message,
			statusErr,
		)
	}

	r.recordEventf(
		myApp,
		corev1.EventTypeWarning,
		reason,
		"%s",
		message,
	)

	if decision.Retry {
		r.recordEventf(
			myApp,
			corev1.EventTypeWarning,
			"RetryScheduled",
			"Retry scheduled after %s because of %s",
			decision.After,
			decision.Reason,
		)

		return ctrl.Result{RequeueAfter: decision.After}, nil
	}

	r.recordEventf(
		myApp,
		corev1.EventTypeWarning,
		"RetrySuppressed",
		"Automatic retry suppressed for permanent error: %s",
		decision.Reason,
	)

	return ctrl.Result{}, nil
}

// recordEventf safely records an Event.
// Recorder can be nil in lightweight unit tests.
func (r *MyAppReconciler) recordEventf(
	myApp *appv1.MyApp,
	eventType string,
	reason string,
	messageFmt string,
	args ...interface{},
) {
	if r.Recorder == nil {
		return
	}

	r.Recorder.Eventf(
		myApp,
		eventType,
		reason,
		messageFmt,
		args...,
	)
}

func (r *MyAppReconciler) handleDeletion(ctx context.Context, myApp *appv1.MyApp, log logr.Logger) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(myApp, myAppFinalizer) {
		return ctrl.Result{}, nil
	}
	log.Info("executing cleanup before deletion", "name", myApp.Name)
	r.recordEventf(myApp, corev1.EventTypeNormal, "Deleting",
		"MyApp %s is being deleted, running cleanup", myApp.Name)
	controllerutil.RemoveFinalizer(myApp, myAppFinalizer)
	if err := r.Update(ctx, myApp); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("finalizer removed, object will be deleted", "name", myApp.Name)
	return ctrl.Result{}, nil
}

func (r *MyAppReconciler) reconcileHPA(ctx context.Context, myApp *appv1.MyApp, log logr.Logger) error {
	key := types.NamespacedName{
		Name:      myApp.Name + "-hpa",
		Namespace: myApp.Namespace,
	}

	var existing autoscalingv2.HorizontalPodAutoscaler
	err := r.Get(ctx, key, &existing)

	autoscalingEnabled :=
		myApp.Spec.Autoscaling != nil &&
			myApp.Spec.Autoscaling.Enabled

	if !autoscalingEnabled {
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		log.Info("deleting hpa because autoscaling is disabled", "name", existing.Name)
		if err := r.Delete(ctx, &existing); err != nil && !apierrors.IsNotFound(err) {
			return err
		}

		r.recordEventf(
			myApp,
			corev1.EventTypeNormal,
			"DeletedHPA",
			"Deleted HPA %s because autoscaling is disabled",
			existing.Name,
		)
		return nil
	}

	desired := buildHPA(myApp, r.Scheme)

	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating hpa", "name", desired.Name)
			r.recordEventf(
				myApp,
				corev1.EventTypeNormal,
				"CreatedHPA",
				"Created HPA %s",
				desired.Name,
			)
			return r.Create(ctx, desired)
		}
		return err
	}

	driftFields := make([]string, 0)

	if !reflect.DeepEqual(
		existing.Spec.ScaleTargetRef,
		desired.Spec.ScaleTargetRef,
	) {
		existing.Spec.ScaleTargetRef = desired.Spec.ScaleTargetRef
		driftFields = append(driftFields, "scaleTargetRef")
	}

	if !reflect.DeepEqual(
		existing.Spec.MinReplicas,
		desired.Spec.MinReplicas,
	) {
		existing.Spec.MinReplicas = desired.Spec.MinReplicas
		driftFields = append(driftFields, "minReplicas")
	}

	if existing.Spec.MaxReplicas != desired.Spec.MaxReplicas {
		existing.Spec.MaxReplicas = desired.Spec.MaxReplicas
		driftFields = append(driftFields, "maxReplicas")
	}

	if !reflect.DeepEqual(existing.Spec.Metrics, desired.Spec.Metrics) {
		existing.Spec.Metrics = desired.Spec.Metrics
		driftFields = append(driftFields, "metrics")
	}

	if len(driftFields) == 0 {
		return nil
	}

	log.Info(
		"correcting hpa drift",
		"name",
		existing.Name,
		"fields",
		driftFields,
	)

	if err := r.Update(ctx, &existing); err != nil {
		return err
	}

	r.recordEventf(
		myApp,
		corev1.EventTypeNormal,
		"HPADriftCorrected",
		"Corrected HPA drift in fields: %v",
		driftFields,
	)

	return nil
}

func (r *MyAppReconciler) reconcileConfigMap(ctx context.Context, myApp *appv1.MyApp, log logr.Logger) error {
	key := types.NamespacedName{
		Name:      myApp.Name + "-config",
		Namespace: myApp.Namespace,
	}

	var existing corev1.ConfigMap
	err := r.Get(ctx, key, &existing)

	if len(myApp.Spec.Config) == 0 {
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		log.Info(
			"deleting configmap because config is empty",
			"name",
			existing.Name,
		)

		if err := r.Delete(ctx, &existing); err != nil && !apierrors.IsNotFound(err) {
			return err
		}

		r.recordEventf(
			myApp,
			corev1.EventTypeNormal,
			"DeletedConfigMap",
			"Deleted ConfigMap %s because MyApp config is empty",
			existing.Name,
		)

		return nil
	}

	desired := buildConfigMap(myApp, r.Scheme)

	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating configmap", "name", desired.Name)
			r.recordEventf(
				myApp,
				corev1.EventTypeNormal,
				"CreatedConfigMap",
				"Created ConfigMap %s",
				desired.Name,
			)
			return r.Create(ctx, desired)
		}
		return err
	}

	driftFields := make([]string, 0)

	if !reflect.DeepEqual(existing.Data, desired.Data) {
		existing.Data = desired.Data
		driftFields = append(driftFields, "data")
	}

	if !reflect.DeepEqual(existing.Labels, desired.Labels) {
		existing.Labels = desired.Labels
		driftFields = append(driftFields, "labels")
	}

	if len(driftFields) == 0 {
		return nil
	}

	log.Info(
		"correcting configmap drift",
		"name",
		existing.Name,
		"fields",
		driftFields,
	)

	if err := r.Update(ctx, &existing); err != nil {
		return err
	}

	r.recordEventf(
		myApp,
		corev1.EventTypeNormal,
		"ConfigMapDriftCorrected",
		"Corrected ConfigMap drift in fields: %v",
		driftFields,
	)

	return nil
}

func (r *MyAppReconciler) reconcileDeployment(
	ctx context.Context,
	myApp *appv1.MyApp,
	log logr.Logger,
) error {
	desired := buildDeployment(myApp, r.Scheme)

	var existing appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{
		Name:      myApp.Name,
		Namespace: myApp.Namespace,
	}, &existing)

	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating deployment", "name", desired.Name)
			r.recordEventf(
				myApp,
				corev1.EventTypeNormal,
				"CreatedDeployment",
				"Created Deployment %s with %d replicas",
				desired.Name,
				myApp.Spec.Replicas,
			)
			return r.Create(ctx, desired)
		}
		return err
	}

	driftFields := make([]string, 0)

	// When HPA is enabled, replicas are controlled by HPA. Enforcing
	// MyApp.spec.replicas here would cause the two controllers to conflict.
	autoscalingEnabled :=
		myApp.Spec.Autoscaling != nil &&
			myApp.Spec.Autoscaling.Enabled

	if !autoscalingEnabled &&
		(existing.Spec.Replicas == nil ||
			*existing.Spec.Replicas != *desired.Spec.Replicas) {
		existing.Spec.Replicas = desired.Spec.Replicas
		driftFields = append(driftFields, "replicas")
	}

	if !reflect.DeepEqual(existing.Labels, desired.Labels) {
		existing.Labels = desired.Labels
		driftFields = append(driftFields, "deploymentLabels")
	}

	if !reflect.DeepEqual(
		existing.Spec.Template.Labels,
		desired.Spec.Template.Labels,
	) {
		existing.Spec.Template.Labels = desired.Spec.Template.Labels
		driftFields = append(driftFields, "podLabels")
	}

	if !reflect.DeepEqual(
		existing.Spec.Strategy,
		desired.Spec.Strategy,
	) {
		existing.Spec.Strategy = desired.Spec.Strategy
		driftFields = append(driftFields, "strategy")
	}

	desiredContainers := desired.Spec.Template.Spec.Containers

	if len(existing.Spec.Template.Spec.Containers) == 0 {
		existing.Spec.Template.Spec.Containers = desiredContainers
		driftFields = append(driftFields, "containers")
	} else {
		currentContainer := &existing.Spec.Template.Spec.Containers[0]
		desiredContainer := desiredContainers[0]

		if currentContainer.Name != desiredContainer.Name {
			currentContainer.Name = desiredContainer.Name
			driftFields = append(driftFields, "containerName")
		}

		if currentContainer.Image != desiredContainer.Image {
			currentContainer.Image = desiredContainer.Image
			driftFields = append(driftFields, "image")
		}

		if !reflect.DeepEqual(
			currentContainer.Ports,
			desiredContainer.Ports,
		) {
			currentContainer.Ports = desiredContainer.Ports
			driftFields = append(driftFields, "containerPorts")
		}

		if !reflect.DeepEqual(
			currentContainer.EnvFrom,
			desiredContainer.EnvFrom,
		) {
			currentContainer.EnvFrom = desiredContainer.EnvFrom
			driftFields = append(driftFields, "envFrom")
		}

		if !reflect.DeepEqual(
			currentContainer.Resources,
			desiredContainer.Resources,
		) {
			currentContainer.Resources = desiredContainer.Resources
			driftFields = append(driftFields, "resources")
		}
	}

	if len(driftFields) == 0 {
		return nil
	}

	log.Info(
		"correcting deployment drift",
		"name",
		existing.Name,
		"fields",
		driftFields,
	)

	if err := r.Update(ctx, &existing); err != nil {
		return err
	}

	r.recordEventf(
		myApp,
		corev1.EventTypeNormal,
		"DeploymentDriftCorrected",
		"Corrected Deployment drift in fields: %v",
		driftFields,
	)

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
			r.recordEventf(
				myApp,
				corev1.EventTypeNormal,
				"CreatedService",
				"Created Service %s on port %d",
				desired.Name,
				myApp.Spec.Port,
			)
			return r.Create(ctx, desired)
		}
		return err
	}

	driftFields := make([]string, 0)

	if !reflect.DeepEqual(existing.Spec.Selector, desired.Spec.Selector) {
		existing.Spec.Selector = desired.Spec.Selector
		driftFields = append(driftFields, "selector")
	}

	if !reflect.DeepEqual(existing.Spec.Ports, desired.Spec.Ports) {
		existing.Spec.Ports = desired.Spec.Ports
		driftFields = append(driftFields, "ports")
	}

	if !reflect.DeepEqual(existing.Labels, desired.Labels) {
		existing.Labels = desired.Labels
		driftFields = append(driftFields, "labels")
	}

	if len(driftFields) == 0 {
		return nil
	}

	log.Info(
		"correcting service drift",
		"name",
		existing.Name,
		"fields",
		driftFields,
	)

	if err := r.Update(ctx, &existing); err != nil {
		return err
	}

	r.recordEventf(
		myApp,
		corev1.EventTypeNormal,
		"ServiceDriftCorrected",
		"Corrected Service drift in fields: %v",
		driftFields,
	)

	return nil
}

func (r *MyAppReconciler) updateStatus(
	ctx context.Context,
	myApp *appv1.MyApp,
	phase string,
	readyReplicas int32,
) error {
	myApp.Status.Phase = phase
	myApp.Status.ObservedGeneration = myApp.Generation
	myApp.Status.ReadyReplicas = readyReplicas

	return r.Status().Update(ctx, myApp)
}

func buildHPA(myApp *appv1.MyApp, scheme *runtime.Scheme) *autoscalingv2.HorizontalPodAutoscaler {
	as := myApp.Spec.Autoscaling

	minReplicas := int32(1)
	if as != nil && as.MinReplicas != nil {
		minReplicas = *as.MinReplicas
	}

	maxReplicas := int32(3)
	if as != nil && as.MaxReplicas != nil {
		maxReplicas = *as.MaxReplicas
	}
	if maxReplicas < minReplicas {
		maxReplicas = minReplicas
	}

	cpuTarget := int32(80)
	if as != nil && as.TargetCPUUtilizationPercentage != nil {
		cpuTarget = *as.TargetCPUUtilizationPercentage
	}

	labels := map[string]string{
		"app":                          myApp.Name,
		"app.kubernetes.io/managed-by": "myapp-operator",
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myApp.Name + "-hpa",
			Namespace: myApp.Namespace,
			Labels:    labels,
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       myApp.Name,
			},
			MinReplicas: &minReplicas,
			MaxReplicas: maxReplicas,
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
	labels := map[string]string{
		"app":                          myApp.Name,
		"app.kubernetes.io/managed-by": "myapp-operator",
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myApp.Name + "-config",
			Namespace: myApp.Namespace,
			Labels:    labels,
		},
		Data: myApp.Spec.Config,
	}

	controllerutil.SetControllerReference(myApp, cm, scheme)
	return cm
}

func buildDeployment(
	myApp *appv1.MyApp,
	scheme *runtime.Scheme,
) *appsv1.Deployment {
	replicas := myApp.Spec.Replicas
	labels := map[string]string{
		"app": myApp.Name,
	}

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

	maxUnavailable := intstr.FromString("25%")
	maxSurge := intstr.FromString("25%")

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myApp.Name,
			Namespace: myApp.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: &maxUnavailable,
					MaxSurge:       &maxSurge,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: image,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: myApp.Spec.Port,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							EnvFrom: envFrom,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU: resource.MustParse(
										"100m",
									),
									corev1.ResourceMemory: resource.MustParse(
										"128Mi",
									),
								},
							},
						},
					},
				},
			},
		},
	}

	controllerutil.SetControllerReference(
		myApp,
		deployment,
		scheme,
	)

	return deployment
}

func buildService(myApp *appv1.MyApp, scheme *runtime.Scheme) *corev1.Service {
	labels := map[string]string{
		"app":                          myApp.Name,
		"app.kubernetes.io/managed-by": "myapp-operator",
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myApp.Name + "-service",
			Namespace: myApp.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": myApp.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
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
