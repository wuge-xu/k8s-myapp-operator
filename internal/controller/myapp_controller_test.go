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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appv1 "k8s-myapp-operator/api/v1"
)

var _ = Describe("MyApp Controller", func() {
	Context("When reconciling a resource", func() {
		const (
			resourceName      = "test-resource"
			resourceNamespace = "default"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}

		BeforeEach(func() {
			By("creating the MyApp custom resource")

			var existing appv1.MyApp
			err := k8sClient.Get(
				ctx,
				typeNamespacedName,
				&existing,
			)

			if err != nil && errors.IsNotFound(err) {
				resource := &appv1.MyApp{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: resourceNamespace,
					},
					Spec: appv1.MyAppSpec{
						Replicas: 1,
						Port:     8080,
						Image:    "nginx:1.27-alpine",
						Config: map[string]string{
							"APP_MODE": "test",
						},
					},
				}

				Expect(
					k8sClient.Create(ctx, resource),
				).To(Succeed())
			}
		})

		AfterEach(func() {
			var resource appv1.MyApp

			err := k8sClient.Get(
				ctx,
				typeNamespacedName,
				&resource,
			)

			if errors.IsNotFound(err) {
				return
			}

			Expect(err).NotTo(HaveOccurred())

			// Avoid leaving the test resource in Terminating because the
			// controller adds a finalizer during reconciliation.
			if len(resource.Finalizers) > 0 {
				resource.Finalizers = nil
				Expect(
					k8sClient.Update(ctx, &resource),
				).To(Succeed())
			}

			Expect(
				k8sClient.Delete(ctx, &resource),
			).To(Succeed())
		})

		It("should create and self-heal the managed Deployment", func() {
			controllerReconciler := &MyAppReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("reconciling the MyApp resource")

			_, err := controllerReconciler.Reconcile(
				ctx,
				reconcile.Request{
					NamespacedName: typeNamespacedName,
				},
			)
			Expect(err).NotTo(HaveOccurred())

			deploymentKey := types.NamespacedName{
				Name:      resourceName,
				Namespace: resourceNamespace,
			}

			var deployment appsv1.Deployment
			Expect(
				k8sClient.Get(
					ctx,
					deploymentKey,
					&deployment,
				),
			).To(Succeed())

			By("verifying the initially created Deployment")

			Expect(deployment.Spec.Replicas).NotTo(BeNil())
			Expect(
				*deployment.Spec.Replicas,
			).To(Equal(int32(1)))

			Expect(
				deployment.Spec.Template.Spec.Containers,
			).To(HaveLen(1))

			container := deployment.Spec.Template.Spec.Containers[0]

			Expect(
				container.Image,
			).To(Equal("nginx:1.27-alpine"))

			Expect(container.Ports).To(HaveLen(1))
			Expect(
				container.Ports[0].ContainerPort,
			).To(Equal(int32(8080)))

			Expect(container.EnvFrom).To(HaveLen(1))
			Expect(
				container.EnvFrom[0].ConfigMapRef,
			).NotTo(BeNil())
			Expect(
				container.EnvFrom[0].ConfigMapRef.Name,
			).To(Equal(resourceName + "-config"))

			Expect(
				deployment.Spec.Template.Labels["app"],
			).To(Equal(resourceName))

			Expect(deployment.OwnerReferences).To(HaveLen(1))
			Expect(
				deployment.OwnerReferences[0].Name,
			).To(Equal(resourceName))

			By("manually introducing Deployment drift")

			driftedReplicas := int32(5)
			deployment.Spec.Replicas = &driftedReplicas

			deployment.Labels = map[string]string{
				"app": "manually-changed",
			}

			// Keep the selector label valid and add a separate drift label.
			deployment.Spec.Template.Labels = map[string]string{
				"app":          resourceName,
				"manual-drift": "true",
			}

			deployment.Spec.Template.Spec.
				Containers[0].Image = "busybox:latest"

			deployment.Spec.Template.Spec.
				Containers[0].Ports = nil

			deployment.Spec.Template.Spec.
				Containers[0].EnvFrom = nil

			deployment.Spec.Strategy = appsv1.DeploymentStrategy{
				Type: appsv1.RecreateDeploymentStrategyType,
			}

			Expect(
				k8sClient.Update(ctx, &deployment),
			).To(Succeed())

			By("reconciling again to repair the drift")

			_, err = controllerReconciler.Reconcile(
				ctx,
				reconcile.Request{
					NamespacedName: typeNamespacedName,
				},
			)
			Expect(err).NotTo(HaveOccurred())

			var healedDeployment appsv1.Deployment
			Expect(
				k8sClient.Get(
					ctx,
					deploymentKey,
					&healedDeployment,
				),
			).To(Succeed())

			By("verifying the Deployment was restored")

			Expect(healedDeployment.Spec.Replicas).NotTo(BeNil())
			Expect(
				*healedDeployment.Spec.Replicas,
			).To(Equal(int32(1)))

			Expect(
				healedDeployment.Labels["app"],
			).To(Equal(resourceName))

			Expect(
				healedDeployment.Spec.Template.Labels,
			).To(Equal(map[string]string{
				"app": resourceName,
			}))

			healedContainer :=
				healedDeployment.Spec.Template.Spec.Containers[0]

			Expect(
				healedContainer.Image,
			).To(Equal("nginx:1.27-alpine"))

			Expect(healedContainer.Ports).To(HaveLen(1))
			Expect(
				healedContainer.Ports[0].ContainerPort,
			).To(Equal(int32(8080)))

			Expect(healedContainer.EnvFrom).To(HaveLen(1))
			Expect(
				healedContainer.EnvFrom[0].ConfigMapRef,
			).NotTo(BeNil())
			Expect(
				healedContainer.EnvFrom[0].ConfigMapRef.Name,
			).To(Equal(resourceName + "-config"))

			Expect(
				healedDeployment.Spec.Strategy.Type,
			).To(Equal(
				appsv1.RollingUpdateDeploymentStrategyType,
			))
		})
	})
})
