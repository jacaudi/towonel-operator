package controller

import (
	"errors"
	"net/http"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/jacaudi/towonel-operator/internal/towonel"
)

// rateLimitRequeue honors a hub 429 Retry-After hint: when err is (or wraps) a
// rate-limited APIError carrying a positive delay, it returns a Result that
// requeues after exactly that delay, so the operator backs off as the hub asked
// instead of adding controller-runtime's generic exponential backoff on top of
// the shared per-tenant rate budget (issue #45). ok is false for every other
// error, leaving the caller's normal error path intact.
func rateLimitRequeue(err error) (ctrl.Result, bool) {
	if apiErr, ok := errors.AsType[*towonel.APIError](err); ok &&
		apiErr.StatusCode == http.StatusTooManyRequests && apiErr.RetryAfter > 0 {
		return ctrl.Result{RequeueAfter: apiErr.RetryAfter}, true
	}
	return ctrl.Result{}, false
}
