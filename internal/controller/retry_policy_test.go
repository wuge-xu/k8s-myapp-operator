package controller

import (
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestClassifyReconcileError(t *testing.T) {
	resource := schema.GroupResource{
		Group:    "apps",
		Resource: "deployments",
	}

	tests := []struct {
		name       string
		err        error
		wantClass  reconcileErrorClass
		wantRetry  bool
		wantAfter  time.Duration
		wantReason string
	}{
		{
			name: "conflict is transient",
			err: apierrors.NewConflict(
				resource,
				"demo",
				errors.New("resource version conflict"),
			),
			wantClass:  reconcileErrorTransient,
			wantRetry:  true,
			wantAfter:  2 * time.Second,
			wantReason: "Conflict",
		},
		{
			name: "too many requests is transient",
			err: apierrors.NewTooManyRequests(
				"rate limited",
				5,
			),
			wantClass:  reconcileErrorTransient,
			wantRetry:  true,
			wantAfter:  15 * time.Second,
			wantReason: "TooManyRequests",
		},
		{
			name: "forbidden is permanent",
			err: apierrors.NewForbidden(
				resource,
				"demo",
				errors.New("permission denied"),
			),
			wantClass:  reconcileErrorPermanent,
			wantRetry:  false,
			wantAfter:  0,
			wantReason: "Forbidden",
		},
		{
			name:       "unknown error is delayed transient",
			err:        errors.New("temporary backend failure"),
			wantClass:  reconcileErrorTransient,
			wantRetry:  true,
			wantAfter:  10 * time.Second,
			wantReason: "UnknownTransientError",
		},
		{
			name:       "nil error needs no retry",
			err:        nil,
			wantClass:  "",
			wantRetry:  false,
			wantAfter:  0,
			wantReason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyReconcileError(tt.err)

			if got.Class != tt.wantClass {
				t.Fatalf(
					"class = %q, want %q",
					got.Class,
					tt.wantClass,
				)
			}

			if got.Retry != tt.wantRetry {
				t.Fatalf(
					"retry = %v, want %v",
					got.Retry,
					tt.wantRetry,
				)
			}

			if got.After != tt.wantAfter {
				t.Fatalf(
					"after = %v, want %v",
					got.After,
					tt.wantAfter,
				)
			}

			if got.Reason != tt.wantReason {
				t.Fatalf(
					"reason = %q, want %q",
					got.Reason,
					tt.wantReason,
				)
			}
		})
	}
}
