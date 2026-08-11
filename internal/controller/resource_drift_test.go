package controller

import (
	"context"
	"testing"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appv1 "k8s-myapp-operator/api/v1"
)

func TestServiceDriftSelfHealing(t *testing.T) {
	ctx := context.Background()
	scheme := newResourceDriftTestScheme(t)

	myApp := newResourceDriftMyApp()
	service := buildService(myApp, scheme)

	service.Spec.Selector = map[string]string{"app": "wrong-app"}
	service.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "wrong",
			Port:       9999,
			TargetPort: intstr.FromInt(9999),
			Protocol:   corev1.ProtocolUDP,
		},
	}
	service.Labels = map[string]string{"manual-drift": "true"}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(myApp.DeepCopy(), service).
		Build()

	reconciler := &MyAppReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	if err := reconciler.reconcileService(
		ctx,
		myApp,
		logf.Log.WithName("test"),
	); err != nil {
		t.Fatalf("reconcile service: %v", err)
	}

	var healed corev1.Service
	if err := fakeClient.Get(
		ctx,
		types.NamespacedName{
			Name:      myApp.Name + "-service",
			Namespace: myApp.Namespace,
		},
		&healed,
	); err != nil {
		t.Fatalf("get service: %v", err)
	}

	if healed.Spec.Selector["app"] != myApp.Name {
		t.Fatalf(
			"selector app = %q, want %q",
			healed.Spec.Selector["app"],
			myApp.Name,
		)
	}

	if len(healed.Spec.Ports) != 1 {
		t.Fatalf("service ports = %d, want 1", len(healed.Spec.Ports))
	}

	port := healed.Spec.Ports[0]

	if port.Name != "http" {
		t.Fatalf("port name = %q, want http", port.Name)
	}

	if port.Port != myApp.Spec.Port {
		t.Fatalf("port = %d, want %d", port.Port, myApp.Spec.Port)
	}

	if port.TargetPort != intstr.FromInt32(myApp.Spec.Port) {
		t.Fatalf(
			"targetPort = %v, want %v",
			port.TargetPort,
			intstr.FromInt32(myApp.Spec.Port),
		)
	}

	if port.Protocol != corev1.ProtocolTCP {
		t.Fatalf("protocol = %s, want TCP", port.Protocol)
	}

	if healed.Labels["app.kubernetes.io/managed-by"] != "myapp-operator" {
		t.Fatal("managed-by label was not restored")
	}
}

func TestConfigMapDriftAndRemoval(t *testing.T) {
	ctx := context.Background()
	scheme := newResourceDriftTestScheme(t)

	t.Run("drift is corrected", func(t *testing.T) {
		myApp := newResourceDriftMyApp()
		configMap := buildConfigMap(myApp, scheme)

		configMap.Data = map[string]string{
			"APP_MODE": "manually-changed",
		}
		configMap.Labels = map[string]string{
			"manual-drift": "true",
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(myApp.DeepCopy(), configMap).
			Build()

		reconciler := &MyAppReconciler{
			Client: fakeClient,
			Scheme: scheme,
		}

		if err := reconciler.reconcileConfigMap(
			ctx,
			myApp,
			logf.Log.WithName("test"),
		); err != nil {
			t.Fatalf("reconcile configmap: %v", err)
		}

		var healed corev1.ConfigMap
		if err := fakeClient.Get(
			ctx,
			types.NamespacedName{
				Name:      myApp.Name + "-config",
				Namespace: myApp.Namespace,
			},
			&healed,
		); err != nil {
			t.Fatalf("get configmap: %v", err)
		}

		if healed.Data["APP_MODE"] != "test" {
			t.Fatalf(
				"APP_MODE = %q, want test",
				healed.Data["APP_MODE"],
			)
		}

		if healed.Labels["app.kubernetes.io/managed-by"] != "myapp-operator" {
			t.Fatal("managed-by label was not restored")
		}
	})

	t.Run("configmap is removed when config becomes empty", func(t *testing.T) {
		myApp := newResourceDriftMyApp()
		configMap := buildConfigMap(myApp, scheme)

		myApp.Spec.Config = nil

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(myApp.DeepCopy(), configMap).
			Build()

		reconciler := &MyAppReconciler{
			Client: fakeClient,
			Scheme: scheme,
		}

		if err := reconciler.reconcileConfigMap(
			ctx,
			myApp,
			logf.Log.WithName("test"),
		); err != nil {
			t.Fatalf("reconcile configmap removal: %v", err)
		}

		var deleted corev1.ConfigMap
		err := fakeClient.Get(
			ctx,
			types.NamespacedName{
				Name:      myApp.Name + "-config",
				Namespace: myApp.Namespace,
			},
			&deleted,
		)

		if err == nil {
			t.Fatal("configmap still exists after config was removed")
		}
	})
}

func TestHPADriftAndDisable(t *testing.T) {
	ctx := context.Background()
	scheme := newResourceDriftTestScheme(t)

	t.Run("drift is corrected", func(t *testing.T) {
		myApp := newResourceDriftMyApp()
		hpa := buildHPA(myApp, scheme)

		wrongMin := int32(1)
		wrongCPU := int32(25)

		hpa.Spec.ScaleTargetRef.Name = "wrong-deployment"
		hpa.Spec.MinReplicas = &wrongMin
		hpa.Spec.MaxReplicas = 99
		hpa.Spec.Metrics = []autoscalingv2.MetricSpec{
			{
				Type: autoscalingv2.ResourceMetricSourceType,
				Resource: &autoscalingv2.ResourceMetricSource{
					Name: corev1.ResourceCPU,
					Target: autoscalingv2.MetricTarget{
						Type:               autoscalingv2.UtilizationMetricType,
						AverageUtilization: &wrongCPU,
					},
				},
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(myApp.DeepCopy(), hpa).
			Build()

		reconciler := &MyAppReconciler{
			Client: fakeClient,
			Scheme: scheme,
		}

		if err := reconciler.reconcileHPA(
			ctx,
			myApp,
			logf.Log.WithName("test"),
		); err != nil {
			t.Fatalf("reconcile hpa: %v", err)
		}

		var healed autoscalingv2.HorizontalPodAutoscaler
		if err := fakeClient.Get(
			ctx,
			types.NamespacedName{
				Name:      myApp.Name + "-hpa",
				Namespace: myApp.Namespace,
			},
			&healed,
		); err != nil {
			t.Fatalf("get hpa: %v", err)
		}

		if healed.Spec.ScaleTargetRef.Name != myApp.Name {
			t.Fatalf(
				"scaleTargetRef = %q, want %q",
				healed.Spec.ScaleTargetRef.Name,
				myApp.Name,
			)
		}

		if healed.Spec.MinReplicas == nil || *healed.Spec.MinReplicas != 2 {
			t.Fatalf("minReplicas was not restored to 2")
		}

		if healed.Spec.MaxReplicas != 10 {
			t.Fatalf(
				"maxReplicas = %d, want 10",
				healed.Spec.MaxReplicas,
			)
		}

		target := healed.Spec.Metrics[0].Resource.Target.AverageUtilization
		if target == nil || *target != 80 {
			t.Fatalf("CPU target was not restored to 80")
		}
	})

	t.Run("hpa is removed when autoscaling is disabled", func(t *testing.T) {
		myApp := newResourceDriftMyApp()
		hpa := buildHPA(myApp, scheme)

		myApp.Spec.Autoscaling.Enabled = false

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(myApp.DeepCopy(), hpa).
			Build()

		reconciler := &MyAppReconciler{
			Client: fakeClient,
			Scheme: scheme,
		}

		if err := reconciler.reconcileHPA(
			ctx,
			myApp,
			logf.Log.WithName("test"),
		); err != nil {
			t.Fatalf("reconcile disabled hpa: %v", err)
		}

		var deleted autoscalingv2.HorizontalPodAutoscaler
		err := fakeClient.Get(
			ctx,
			types.NamespacedName{
				Name:      myApp.Name + "-hpa",
				Namespace: myApp.Namespace,
			},
			&deleted,
		)

		if err == nil {
			t.Fatal("hpa still exists after autoscaling was disabled")
		}
	})
}

func TestBuildHPAUsesSafeDefaults(t *testing.T) {
	scheme := newResourceDriftTestScheme(t)

	myApp := &appv1.MyApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-hpa",
			Namespace: "default",
		},
		Spec: appv1.MyAppSpec{
			Replicas: 1,
			Port:     8080,
			Autoscaling: &appv1.AutoscalingSpec{
				Enabled: true,
			},
		},
	}

	hpa := buildHPA(myApp, scheme)

	if hpa.Spec.MinReplicas == nil || *hpa.Spec.MinReplicas != 1 {
		t.Fatal("default minReplicas should be 1")
	}

	if hpa.Spec.MaxReplicas != 3 {
		t.Fatalf(
			"default maxReplicas = %d, want 3",
			hpa.Spec.MaxReplicas,
		)
	}

	target := hpa.Spec.Metrics[0].Resource.Target.AverageUtilization
	if target == nil || *target != 80 {
		t.Fatal("default CPU target should be 80")
	}
}

func newResourceDriftTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()

	if err := appv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add MyApp scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := autoscalingv2.AddToScheme(scheme); err != nil {
		t.Fatalf("add autoscaling scheme: %v", err)
	}

	return scheme
}

func newResourceDriftMyApp() *appv1.MyApp {
	minReplicas := int32(2)
	maxReplicas := int32(10)
	cpuTarget := int32(80)

	return &appv1.MyApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "resource-drift-test",
			Namespace: "default",
		},
		Spec: appv1.MyAppSpec{
			Replicas: 2,
			Port:     8080,
			Image:    "nginx:1.27-alpine",
			Config: map[string]string{
				"APP_MODE": "test",
			},
			Autoscaling: &appv1.AutoscalingSpec{
				Enabled:                        true,
				MinReplicas:                    &minReplicas,
				MaxReplicas:                    &maxReplicas,
				TargetCPUUtilizationPercentage: &cpuTarget,
			},
		},
	}
}
