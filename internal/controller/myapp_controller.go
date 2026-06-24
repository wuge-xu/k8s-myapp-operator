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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

func (r *MyAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 第一步：获取 MyApp 对象
	var myApp appv1.MyApp
	if err := r.Get(ctx, req.NamespacedName, &myApp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 第二步：构造期望的 Deployment
	desiredDeployment := buildDeployment(&myApp, r.Scheme)

	// 第三步：查找是否已有对应 Deployment
	var existingDeployment appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{
		Name:      myApp.Name,
		Namespace: myApp.Namespace,
	}, &existingDeployment)

	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("creating new deployment", "name", desiredDeployment.Name)
			if err := r.Create(ctx, desiredDeployment); err != nil {
				return ctrl.Result{}, err
			}
			// 刚创建完，Deployment 还没有真实状态，先同步一个 Pending 状态
			return r.updateStatus(ctx, &myApp, "Pending", 0)
		}
		return ctrl.Result{}, err
	}

	// 第四步：如果副本数不一致，更新 Deployment
	if *existingDeployment.Spec.Replicas != myApp.Spec.Replicas {
		log.Info("updating deployment replicas",
			"name", existingDeployment.Name,
			"from", *existingDeployment.Spec.Replicas,
			"to", myApp.Spec.Replicas,
		)
		existingDeployment.Spec.Replicas = &myApp.Spec.Replicas
		if err := r.Update(ctx, &existingDeployment); err != nil {
			return ctrl.Result{}, err
		}
	}

	// 第五步：根据 Deployment 的真实状态，同步到 MyApp.Status
	// existingDeployment.Status.ReadyReplicas 是 K8s 自动维护的字段，
	// 表示"当前有多少个 Pod 已经通过了健康检查、处于 Ready 状态"
	readyReplicas := existingDeployment.Status.ReadyReplicas
	phase := "Pending"
	if readyReplicas == myApp.Spec.Replicas {
		phase = "Running"
	} else if readyReplicas > 0 {
		phase = "Degraded"
	}

	return r.updateStatus(ctx, &myApp, phase, readyReplicas)
}

// updateStatus 把 phase 和 readyReplicas 写回 MyApp 的 status 字段
// 注意：这里用的是 r.Status().Update()，而不是 r.Update()
// 原因：MyApp 开启了 status subresource，status 字段必须通过专门的 status 接口更新
// 如果用普通的 r.Update()，K8s 会忽略 status 字段的变化，根本不会生效
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

	labels := map[string]string{
		"app": myApp.Name,
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myApp.Name,
			Namespace: myApp.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}

	controllerutil.SetControllerReference(myApp, deployment, scheme)
	return deployment
}

func (r *MyAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appv1.MyApp{}).
		Owns(&appsv1.Deployment{}).
		Named("myapp").
		Complete(r)
}
