package controller

import (
	"context"
	"errors"
	"net/http"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/towonel"
)

func (r *TowonelTunnelReconciler) reconcileDelete(ctx context.Context, tt *towonelv1alpha1.TowonelTunnel) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(tt, FinalizerName) {
		return ctrl.Result{}, nil
	}
	if tt.Spec.DeletionPolicy != towonelv1alpha1.DeletionPolicyRetain && tt.Status.InviteID != "" {
		apiKey, halt, err := r.resolveAPIKey(ctx, tt)
		if err != nil {
			return ctrl.Result{}, err
		}
		if halt {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil // can't auth; keep finalizer
		}
		callCtx, cancel := context.WithTimeout(ctx, hubCallTimeout)
		defer cancel()
		tc := towonel.NewClient(r.BaseURL, apiKey.Expose(), r.HTTPClient)
		if err := tc.DeleteInvite(callCtx, tt.Status.InviteID); err != nil {
			if apiErr, ok := errors.AsType[*towonel.APIError](err); !ok || apiErr.StatusCode != http.StatusNotFound {
				return ctrl.Result{}, err
			} // 404 -> already gone, idempotent
		}
	}
	controllerutil.RemoveFinalizer(tt, FinalizerName)
	return ctrl.Result{}, r.Update(ctx, tt)
}

// writeStatus persists status via read-modify-write, gated on a semantic diff.
// A conflict is returned (not swallowed) so the caller requeues promptly.
func (r *TowonelTunnelReconciler) writeStatus(ctx context.Context, tt *towonelv1alpha1.TowonelTunnel, orig *towonelv1alpha1.TowonelTunnelStatus) error {
	tt.Status.ObservedGeneration = tt.Generation
	if equality.Semantic.DeepEqual(orig, &tt.Status) {
		return nil
	}
	return r.Status().Update(ctx, tt)
}

func (r *TowonelTunnelReconciler) fail(ctx context.Context, tt *towonelv1alpha1.TowonelTunnel, orig *towonelv1alpha1.TowonelTunnelStatus, err error) (ctrl.Result, error) {
	setCond(tt, CondReady, metav1.ConditionFalse, ReasonAPIError, err.Error())
	tt.Status.Phase = "Error"
	_ = r.writeStatus(ctx, tt, orig) // defensive; surface the real error
	return ctrl.Result{}, err
}
