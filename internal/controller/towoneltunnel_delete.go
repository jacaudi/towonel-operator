package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
		if tt.Status.TenantID != "" {
			// Raw ctx, not callCtx: hubCall applies per-call deadlines internally;
			// sharing one 20s window would starve DeleteInvite on many-port tunnels.
			if err := r.releasePorts(ctx, tc, tt); err != nil {
				return ctrl.Result{}, err
			}
		}
		if err := tc.DeleteInvite(callCtx, tt.Status.InviteID); err != nil {
			if apiErr, ok := errors.AsType[*towonel.APIError](err); !ok || apiErr.StatusCode != http.StatusNotFound {
				return ctrl.Result{}, err
			} // 404 -> already gone, idempotent
		}
	}
	controllerutil.RemoveFinalizer(tt, FinalizerName)
	return ctrl.Result{}, r.Update(ctx, tt)
}

// releasePorts releases all of this tunnel's reservations on Delete policy:
// everything in status, PLUS a best-effort label-prefix sweep over ListPorts
// for reservations that never reached status (design §4.B deletion).
func (r *TowonelTunnelReconciler) releasePorts(ctx context.Context, tc *towonel.Client, tt *towonelv1alpha1.TowonelTunnel) error {
	release := func(protocol string, port int32) error {
		_, err := hubCall(ctx, func(c context.Context) (struct{}, error) {
			return struct{}{}, tc.ReleasePort(c, tt.Status.TenantID, protocol, port)
		})
		if err != nil {
			if apiErr, ok := errors.AsType[*towonel.APIError](err); ok && apiErr.StatusCode == http.StatusNotFound {
				return nil
			}
			return err
		}
		return nil
	}
	released := map[string]bool{}
	for _, pa := range tt.Status.PortAllocations {
		if err := release(pa.Protocol, pa.ListenPort); err != nil {
			return fmt.Errorf("release port %s/%s: %w", pa.Protocol, pa.Name, err)
		}
		released[fmt.Sprintf("%s/%d", pa.Protocol, pa.ListenPort)] = true
	}
	// Leak sweep (list shape UNVERIFIED -> tolerate failure, design §7 residual).
	listed, err := hubCall(ctx, func(c context.Context) ([]towonel.ReservePortResponse, error) {
		return tc.ListPorts(c, tt.Status.TenantID)
	})
	if err != nil {
		return nil // best-effort only
	}
	prefix := portLabelPrefix(tt.Namespace, tt.Name)
	for i := range listed {
		if listed[i].Label != nil && strings.HasPrefix(*listed[i].Label, prefix) {
			if released[fmt.Sprintf("%s/%d", listed[i].Protocol, listed[i].Port)] {
				continue // already released via status above
			}
			if err := release(listed[i].Protocol, listed[i].Port); err != nil {
				return fmt.Errorf("sweep release %s/%d: %w", listed[i].Protocol, listed[i].Port, err)
			}
		}
	}
	return nil
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
