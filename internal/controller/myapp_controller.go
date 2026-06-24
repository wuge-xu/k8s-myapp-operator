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

type MyAppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=app.demo.io,resources=myapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=app.demo.io,resources=myapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=app.demo.io,resources=myapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *MyAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var myApp appv1.MyApp
	if err := r.Get(ctx, req.NamespacedName, &myApp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
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

func buildDeployment(myApp *appv1.MyApp, scheme *runtime.Scheme) *appsv1.Deployment {
	replicas := myApp.Spec.Replicas
	labels := map[string]string{"app": myApp.Name}

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
		Named("myapp").
		Complete(r)
}
