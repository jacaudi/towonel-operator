package controller

import (
	"context"
	"errors"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	record "k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// TowonelAgentReconciler reconciles a TowonelAgent object. It makes ZERO hub
// calls — all Towonel API interaction is tunnel-side (design §2 invariant).
type TowonelAgentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=towonel.io,resources=towonelagents,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=towonel.io,resources=towonelagents/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=towonel.io,resources=towonelagents/finalizers,verbs=update
//+kubebuilder:rbac:groups="apps",resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;list;watch
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterrolebindings,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile drives a TowonelAgent toward its desired state. No finalizer:
// children die by ownerRef GC; the tunnel re-aggregates via watch (design §4.H).
func (r *TowonelAgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var ta towonelv1alpha1.TowonelAgent
	if err := r.Get(ctx, req.NamespacedName, &ta); err != nil {
		if apierrors.IsNotFound(err) {
			// Agent deleted: recompute the shared node-reader subjects so its SA
			// subject is dropped (no finalizer — design §5.3).
			if _, rerr := r.reconcileNodeReaderSubjects(ctx); rerr != nil {
				return ctrl.Result{}, rerr
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !ta.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	orig := ta.Status.DeepCopy()
	tunnel, token, gate, err := r.readTunnelToken(ctx, &ta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if gate != nil {
		// Other conditions are intentionally left at last-known state —
		// the children (Secret/Deployment) still exist while waiting.
		setAgentCond(&ta, CondTunnelReady, metav1.ConditionFalse, gate.reason, gate.message)
		markAgentWaiting(&ta)
		if werr := r.writeStatus(ctx, &ta, orig); werr != nil {
			return ctrl.Result{}, werr
		}
		// Watch is the prompt wake-up; requeue is the staleness fallback (§4.C).
		return ctrl.Result{RequeueAfter: waitingRequeue}, nil
	}
	setAgentCond(&ta, CondTunnelReady, metav1.ConditionTrue, ReasonReady, "tunnel token available")

	if err := r.ensureAgentSecret(ctx, &ta, token, tunnel.Status.InviteID); err != nil {
		if errors.Is(err, errSecretClash) {
			setAgentCond(&ta, CondConfigRendered, metav1.ConditionFalse, ReasonSecretClash, err.Error())
			ta.Status.Phase = "Pending"
			if r.Recorder != nil {
				r.Recorder.Event(&ta, corev1.EventTypeWarning, ReasonSecretClash, err.Error())
			}
			if werr := r.writeStatus(ctx, &ta, orig); werr != nil {
				return ctrl.Result{}, werr
			}
			return ctrl.Result{RequeueAfter: waitingRequeue}, nil
		}
		return r.fail(ctx, &ta, orig, err)
	}

	// Connectivity (P6) — optional, never wedges. Apply BEFORE the Deployment so
	// the pod's serviceAccountName resolves.
	plan := planConnectivity(&ta)
	shellMissing, cErr := r.ensureConnectivity(ctx, &ta, plan)
	if cErr != nil {
		return r.fail(ctx, &ta, orig, cErr)
	}
	setConnectivityCond(&ta, plan, connectivityRequested(&ta), shellMissing)
	if r.Recorder != nil {
		if plan.skipped {
			r.Recorder.Event(&ta, corev1.EventTypeWarning, ReasonConnectivitySkipped, plan.skipReason)
		}
		if plan.portIgnored {
			r.Recorder.Event(&ta, corev1.EventTypeNormal, ReasonPortIgnored, "nodePort.port ignored: nodePort.create is false")
		}
		if shellMissing && plan.autodiscover {
			r.Recorder.Event(&ta, corev1.EventTypeWarning, ReasonNodeRBACShellMissing, "chart node-RBAC shell missing; enable agentNodeRBAC.create")
		}
	}

	cfg, err := renderConfig(&ta, tunnel.Status.PortAllocations, tunnel.Status.InviteID)
	if err != nil {
		return r.fail(ctx, &ta, orig, err)
	}
	dep, err := r.ensureDeployment(ctx, &ta, cfg)
	if err != nil {
		return r.fail(ctx, &ta, orig, err)
	}
	setAgentCond(&ta, CondConfigRendered, metav1.ConditionTrue, ReasonRendered, "secret and deployment rendered")

	rollupAgentStatus(&ta, cfg, dep)
	if err := r.writeStatus(ctx, &ta, orig); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("reconciled", "phase", ta.Status.Phase, "configHash", ta.Status.ObservedConfigHash)
	return ctrl.Result{}, nil // steady state: fully watch-driven
}

// agentsForTunnel maps a tunnel event to every referencing agent (field index).
func (r *TowonelAgentReconciler) agentsForTunnel(ctx context.Context, obj client.Object) []reconcile.Request {
	var list towonelv1alpha1.TowonelAgentList
	if err := r.List(ctx, &list, client.MatchingFields{agentTunnelRefIndex: client.ObjectKeyFromObject(obj).String()}); err != nil {
		logf.FromContext(ctx).Error(err, "agentsForTunnel: list failed", "tunnel", client.ObjectKeyFromObject(obj))
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
	}
	return reqs
}

// SetupWithManager wires the reconciler to the manager.
func (r *TowonelAgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&towonelv1alpha1.TowonelAgent{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Service{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Watches(&towonelv1alpha1.TowonelTunnel{}, handler.EnqueueRequestsFromMapFunc(r.agentsForTunnel)).
		Named("towonelagent").
		Complete(r)
}
