package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	appv1 "k8s-myapp-operator/api/v1"
)

func TestFailReconcileTransientError(t *testing.T) {
	ctx := context.Background()

	scheme := runtime.NewScheme()
	if err := appv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add MyApp scheme: %v", err)
	}

	myApp := &appv1.MyApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "transient-test",
			Namespace:  "default",
			Generation: 3,
		},
		Spec: appv1.MyAppSpec{
			Replicas: 1,
			Port:     8080,
			Image:    "nginx:latest",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&appv1.MyApp{}).
		WithObjects(myApp.DeepCopy()).
		Build()

	recorder := record.NewFakeRecorder(20)

	reconciler := &MyAppReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: recorder,
	}

	var current appv1.MyApp
	if err := fakeClient.Get(
		ctx,
		types.NamespacedName{
			Name:      myApp.Name,
			Namespace: myApp.Namespace,
		},
		&current,
	); err != nil {
		t.Fatalf("get MyApp: %v", err)
	}

	resource := schema.GroupResource{
		Group:    "apps",
		Resource: "deployments",
	}

	reconcileErr := apierrors.NewConflict(
		resource,
		myApp.Name,
		errors.New("resource version conflict"),
	)

	result, err := reconciler.failReconcile(
		ctx,
		&current,
		"DeploymentReconcileFailed",
		reconcileErr,
	)

	if err != nil {
		t.Fatalf(
			"failReconcile returned unexpected error: %v",
			err,
		)
	}

	if result.RequeueAfter != 2*time.Second {
		t.Fatalf(
			"RequeueAfter = %v, want %v",
			result.RequeueAfter,
			2*time.Second,
		)
	}

	var updated appv1.MyApp
	if err := fakeClient.Get(
		ctx,
		types.NamespacedName{
			Name:      myApp.Name,
			Namespace: myApp.Namespace,
		},
		&updated,
	); err != nil {
		t.Fatalf("get updated MyApp: %v", err)
	}

	if updated.Status.Phase != "Degraded" {
		t.Fatalf(
			"phase = %q, want Degraded",
			updated.Status.Phase,
		)
	}

	if updated.Status.ObservedGeneration != 3 {
		t.Fatalf(
			"observedGeneration = %d, want 3",
			updated.Status.ObservedGeneration,
		)
	}

	degraded := meta.FindStatusCondition(
		updated.Status.Conditions,
		conditionTypeDegraded,
	)

	if degraded == nil {
		t.Fatal("Degraded condition was not persisted")
	}

	if degraded.Status != metav1.ConditionTrue {
		t.Fatalf(
			"Degraded status = %s, want True",
			degraded.Status,
		)
	}

	assertFakeEventContains(
		t,
		recorder,
		"RetryScheduled",
	)
}

func TestFailReconcilePermanentError(t *testing.T) {
	ctx := context.Background()

	scheme := runtime.NewScheme()
	if err := appv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add MyApp scheme: %v", err)
	}

	myApp := &appv1.MyApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "permanent-test",
			Namespace:  "default",
			Generation: 4,
		},
		Spec: appv1.MyAppSpec{
			Replicas: 1,
			Port:     8080,
			Image:    "nginx:latest",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&appv1.MyApp{}).
		WithObjects(myApp.DeepCopy()).
		Build()

	recorder := record.NewFakeRecorder(20)

	reconciler := &MyAppReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: recorder,
	}

	var current appv1.MyApp
	if err := fakeClient.Get(
		ctx,
		types.NamespacedName{
			Name:      myApp.Name,
			Namespace: myApp.Namespace,
		},
		&current,
	); err != nil {
		t.Fatalf("get MyApp: %v", err)
	}

	resource := schema.GroupResource{
		Group:    "apps",
		Resource: "deployments",
	}

	reconcileErr := apierrors.NewForbidden(
		resource,
		myApp.Name,
		errors.New("permission denied"),
	)

	result, err := reconciler.failReconcile(
		ctx,
		&current,
		"DeploymentReconcileFailed",
		reconcileErr,
	)

	if err != nil {
		t.Fatalf(
			"failReconcile returned unexpected error: %v",
			err,
		)
	}

	if result.RequeueAfter != 0 {
		t.Fatalf(
			"RequeueAfter = %v, want 0",
			result.RequeueAfter,
		)
	}

	if result.Requeue {
		t.Fatal(
			"permanent error should not request immediate requeue",
		)
	}

	var updated appv1.MyApp
	if err := fakeClient.Get(
		ctx,
		types.NamespacedName{
			Name:      myApp.Name,
			Namespace: myApp.Namespace,
		},
		&updated,
	); err != nil {
		t.Fatalf("get updated MyApp: %v", err)
	}

	if updated.Status.Phase != "Degraded" {
		t.Fatalf(
			"phase = %q, want Degraded",
			updated.Status.Phase,
		)
	}

	if updated.Status.ObservedGeneration != 4 {
		t.Fatalf(
			"observedGeneration = %d, want 4",
			updated.Status.ObservedGeneration,
		)
	}

	degraded := meta.FindStatusCondition(
		updated.Status.Conditions,
		conditionTypeDegraded,
	)

	if degraded == nil {
		t.Fatal("Degraded condition was not persisted")
	}

	if degraded.Status != metav1.ConditionTrue {
		t.Fatalf(
			"Degraded status = %s, want True",
			degraded.Status,
		)
	}

	assertFakeEventContains(
		t,
		recorder,
		"RetrySuppressed",
	)
}

func assertFakeEventContains(
	t *testing.T,
	recorder *record.FakeRecorder,
	expected string,
) {
	t.Helper()

	timeout := time.After(2 * time.Second)

	for {
		select {
		case event := <-recorder.Events:
			if strings.Contains(event, expected) {
				return
			}

		case <-timeout:
			t.Fatalf(
				"did not receive event containing %q",
				expected,
			)
		}
	}
}
