// Package controller holds the towonel-operator reconcilers.
package controller

import (
	"context"
	"errors"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	record "k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/towonel"
)

// TowonelTunnelReconciler reconciles a TowonelTunnel object.
type TowonelTunnelReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Recorder   record.EventRecorder
	BaseURL    string
	HTTPClient *http.Client
}

//+kubebuilder:rbac:groups=towonel.io,resources=towoneltunnels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=towonel.io,resources=towoneltunnels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=towonel.io,resources=towoneltunnels/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile drives a TowonelTunnel toward its desired state.
func (r *TowonelTunnelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var tt towonelv1alpha1.TowonelTunnel
	if err := r.Get(ctx, req.NamespacedName, &tt); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !tt.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &tt)
	}
	if controllerutil.AddFinalizer(&tt, FinalizerName) {
		if err := r.Update(ctx, &tt); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	orig := tt.Status.DeepCopy()
	apiKey, halt, err := r.resolveAPIKey(ctx, &tt)
	if err != nil {
		return ctrl.Result{}, err
	}
	if halt {
		setCond(&tt, CondReady, metav1.ConditionFalse, ReasonInvalidConfig, "no Towonel API key (spec.apiKeySecretRef or TOWONEL_API_KEY)")
		tt.Status.Phase = "Error"
		if r.Recorder != nil {
			r.Recorder.Event(&tt, corev1.EventTypeWarning, ReasonInvalidConfig, "no Towonel API key configured")
		}
		if werr := r.writeStatus(ctx, &tt, orig); werr != nil {
			return ctrl.Result{}, werr
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, hubCallTimeout)
	defer cancel()
	tc := towonel.NewClient(r.BaseURL, apiKey.Expose(), r.HTTPClient)

	token, err := r.ensureInvite(callCtx, tc, &tt)
	if err != nil {
		return r.fail(ctx, &tt, orig, err)
	}
	if err := r.ensureTokenSecret(ctx, &tt, token); err != nil {
		if errors.Is(err, errAdoptedNoToken) {
			setCond(&tt, CondReady, metav1.ConditionFalse, ReasonReconciling, err.Error())
			tt.Status.Phase = "Pending"
			if werr := r.writeStatus(ctx, &tt, orig); werr != nil {
				return ctrl.Result{}, werr
			}
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		}
		return r.fail(ctx, &tt, orig, err)
	}
	if err := r.convergeHostnames(callCtx, tc, &tt); err != nil {
		setCond(&tt, CondHostnamesSynced, metav1.ConditionFalse, ReasonAPIError, err.Error())
		return r.fail(ctx, &tt, orig, err)
	}
	setCond(&tt, CondHostnamesSynced, metav1.ConditionTrue, ReasonSynced, "authorized hostnames converged")
	rollupStatus(&tt, time.Now())
	if err := r.writeStatus(ctx, &tt, orig); err != nil {
		return ctrl.Result{}, err // conflict surfaces here -> prompt requeue
	}
	log.Info("reconciled", "invite", tt.Status.InviteID)
	return ctrl.Result{RequeueAfter: 30 * time.Minute}, nil
}

// SetupWithManager wires the reconciler to the manager.
func (r *TowonelTunnelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&towonelv1alpha1.TowonelTunnel{}).
		Owns(&corev1.Secret{}).
		Named("towoneltunnel").
		Complete(r)
}
