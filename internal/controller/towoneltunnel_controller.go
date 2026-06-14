// Package controller holds the towonel-operator reconcilers.
package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	record "k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	handler "sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
// Leader election (manager-level, default-on): controller-runtime's Lease lock.
//+kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

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

	tc := towonel.NewClient(r.BaseURL, apiKey.Expose(), r.HTTPClient)

	agents, err := r.listReferencingAgents(ctx, &tt)
	if err != nil {
		return r.fail(ctx, &tt, orig, err)
	}
	desired := desiredHostnames(&tt, agents)

	// #14(a): the Towonel API requires >=1 hostname to create an invite, and the
	// tenant (needed for port reservation) only exists after creation. Defer until
	// a hostname exists rather than spinning on a 400.
	if len(desired) == 0 && tt.Status.InviteID == "" {
		setCond(&tt, CondReady, metav1.ConditionFalse, ReasonPending,
			"no authorized hostnames yet — a TowonelTunnel needs at least one HTTPS hostname (spec.extraHostnames or an agent HTTPS service) to create its invite, even for TCP/UDP-only routing")
		tt.Status.Phase = "Pending"
		if werr := r.writeStatus(ctx, &tt, orig); werr != nil {
			return ctrl.Result{}, werr
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	inviteCtx, cancelInvite := context.WithTimeout(ctx, hubCallTimeout)
	token, err := r.ensureInvite(inviteCtx, tc, &tt, desired)
	cancelInvite()
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

	hostCtx, cancelHost := context.WithTimeout(ctx, hubCallTimeout)
	err = r.convergeHostnames(hostCtx, tc, &tt, desired)
	cancelHost()
	if err != nil {
		setCond(&tt, CondHostnamesSynced, metav1.ConditionFalse, ReasonAPIError, err.Error())
		return r.fail(ctx, &tt, orig, err)
	}
	setCond(&tt, CondHostnamesSynced, metav1.ConditionTrue, ReasonSynced, "authorized hostnames converged")
	if err := r.convergePorts(ctx, tc, &tt, agents); err != nil { // sets PortsReserved itself
		return r.fail(ctx, &tt, orig, err)
	}
	rollupStatus(&tt, time.Now())
	if err := r.writeStatus(ctx, &tt, orig); err != nil {
		return ctrl.Result{}, err // conflict surfaces here -> prompt requeue
	}
	log.Info("reconciled", "invite", tt.Status.InviteID)
	return ctrl.Result{RequeueAfter: 30 * time.Minute}, nil
}

// listReferencingAgents returns agents whose resolved tunnelRef is this tunnel,
// sorted by ns/name (deterministic aggregation, design §4.B).
func (r *TowonelTunnelReconciler) listReferencingAgents(ctx context.Context, tt *towonelv1alpha1.TowonelTunnel) ([]towonelv1alpha1.TowonelAgent, error) {
	var list towonelv1alpha1.TowonelAgentList
	key := types.NamespacedName{Namespace: tt.Namespace, Name: tt.Name}.String()
	if err := r.List(ctx, &list, client.MatchingFields{agentTunnelRefIndex: key}); err != nil {
		return nil, fmt.Errorf("list referencing agents: %w", err)
	}
	slices.SortFunc(list.Items, func(a, b towonelv1alpha1.TowonelAgent) int {
		return cmp.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})
	return list.Items, nil
}

// SetupWithManager wires the reconciler to the manager.
func (r *TowonelTunnelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&towonelv1alpha1.TowonelTunnel{}).
		Owns(&corev1.Secret{}).
		Watches(&towonelv1alpha1.TowonelAgent{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, obj client.Object) []reconcile.Request {
				ta := obj.(*towonelv1alpha1.TowonelAgent)
				if ta.Spec.TunnelRef.Name == "" {
					return nil
				}
				return []reconcile.Request{{NamespacedName: resolvedTunnelRef(ta)}}
			})).
		Named("towoneltunnel").
		Complete(r)
}
