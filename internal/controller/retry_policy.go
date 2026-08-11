package controller

import (
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type reconcileErrorClass string

const (
	reconcileErrorTransient reconcileErrorClass = "Transient"
	reconcileErrorPermanent reconcileErrorClass = "Permanent"
)

type retryDecision struct {
	Class  reconcileErrorClass
	Retry  bool
	After  time.Duration
	Reason string
}

func classifyReconcileError(err error) retryDecision {
	if err == nil {
		return retryDecision{}
	}

	switch {
	case apierrors.IsConflict(err):
		return retryDecision{
			Class:  reconcileErrorTransient,
			Retry:  true,
			After:  2 * time.Second,
			Reason: "Conflict",
		}

	case apierrors.IsTooManyRequests(err):
		return retryDecision{
			Class:  reconcileErrorTransient,
			Retry:  true,
			After:  15 * time.Second,
			Reason: "TooManyRequests",
		}

	case apierrors.IsTimeout(err):
		return retryDecision{
			Class:  reconcileErrorTransient,
			Retry:  true,
			After:  10 * time.Second,
			Reason: "Timeout",
		}

	case apierrors.IsServerTimeout(err):
		return retryDecision{
			Class:  reconcileErrorTransient,
			Retry:  true,
			After:  10 * time.Second,
			Reason: "ServerTimeout",
		}

	case apierrors.IsServiceUnavailable(err):
		return retryDecision{
			Class:  reconcileErrorTransient,
			Retry:  true,
			After:  10 * time.Second,
			Reason: "ServiceUnavailable",
		}

	case apierrors.IsUnauthorized(err):
		return retryDecision{
			Class:  reconcileErrorPermanent,
			Retry:  false,
			Reason: "Unauthorized",
		}

	case apierrors.IsForbidden(err):
		return retryDecision{
			Class:  reconcileErrorPermanent,
			Retry:  false,
			Reason: "Forbidden",
		}

	case apierrors.IsInvalid(err):
		return retryDecision{
			Class:  reconcileErrorPermanent,
			Retry:  false,
			Reason: "Invalid",
		}

	case apierrors.IsBadRequest(err):
		return retryDecision{
			Class:  reconcileErrorPermanent,
			Retry:  false,
			Reason: "BadRequest",
		}

	default:
		// 对未知错误采用保守策略。
		// 大多数 Kubernetes/API/网络异常可能是暂时性的，
		// 因此允许延迟重试，但避免立即高速循环。
		return retryDecision{
			Class:  reconcileErrorTransient,
			Retry:  true,
			After:  10 * time.Second,
			Reason: "UnknownTransientError",
		}
	}
}
