package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	record "k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
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
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	// AgentEnv is the operator-wide extra env (--agent-env, parsed by
	// ParseAgentEnv) added to every agent pod, hand-authored or auto-created.
	AgentEnv []corev1.EnvVar
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
	var result ctrl.Result
	var missing bool
	var reconciled *towonelv1alpha1.TowonelAgent
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		result = ctrl.Result{}
		missing = false
		reconciled = nil
		var ta towonelv1alpha1.TowonelAgent
		agentReader := client.Reader(r.Client)
		if r.APIReader != nil {
			agentReader = r.APIReader
		}
		if err := agentReader.Get(ctx, req.NamespacedName, &ta); err != nil {
			if apierrors.IsNotFound(err) {
				missing = true
				return nil
			}
			return err
		}
		if !ta.DeletionTimestamp.IsZero() {
			return nil
		}
		orig := ta.Status.DeepCopy()

		tunnel, token, gate, err := r.readTunnelToken(ctx, &ta)
		if err != nil {
			return err
		}
		if gate != nil {
			// Other conditions remain at last-known state while the children wait.
			setAgentCond(&ta, CondTunnelReady, metav1.ConditionFalse, gate.reason, gate.message)
			markAgentWaiting(&ta)
			if err := r.writeStatus(ctx, &ta, orig); err != nil {
				return err
			}
			result = ctrl.Result{RequeueAfter: waitingRequeue}
			reconciled = &ta
			return nil
		}
		setAgentCond(&ta, CondTunnelReady, metav1.ConditionTrue, ReasonReady, "tunnel token available")

		if err := r.ensureAgentSecret(ctx, &ta, token, tunnel.Status.InviteID); err != nil {
			if !errors.Is(err, errSecretClash) {
				_, failure := r.failStatus(ctx, &ta, orig, err)
				return failure
			}
			if errors.Is(err, errSecretClash) {
				setAgentCond(&ta, CondConfigRendered, metav1.ConditionFalse, ReasonSecretClash, err.Error())
				ta.Status.Phase = "Pending"
				if r.Recorder != nil {
					r.Recorder.Event(&ta, corev1.EventTypeWarning, ReasonSecretClash, err.Error())
				}
				if err := r.writeStatus(ctx, &ta, orig); err != nil {
					return err
				}
				result = ctrl.Result{RequeueAfter: waitingRequeue}
				reconciled = &ta
				return nil
			}
		}

		// Connectivity is optional; apply it before the Deployment so its
		// service account resolves.
		plan := planConnectivity(&ta)
		shellMissing, cErr := r.ensureConnectivity(ctx, &ta, plan)
		if cErr != nil {
			_, failure := r.failStatus(ctx, &ta, orig, cErr)
			return failure
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
			_, failure := r.failStatus(ctx, &ta, orig, err)
			return failure
		}
		if cfg, err = cfg.withExtraEnv(&ta, r.AgentEnv); err != nil {
			_, failure := r.failStatus(ctx, &ta, orig, err)
			return failure
		}
		if r.Recorder != nil && len(cfg.IgnoredEnv) > 0 {
			r.Recorder.Eventf(&ta, corev1.EventTypeWarning, ReasonEnvIgnored,
				"--agent-env entries ignored (operator-managed names): %s", strings.Join(cfg.IgnoredEnv, ", "))
		}
		// Deployment SSA has no lock on its inputs; status writes are resourceVersion-locked.
		if err := r.checkAgentUnchanged(ctx, &ta); err != nil {
			return err
		}
		if err := r.checkTunnelUnchanged(ctx, tunnel); err != nil {
			return err
		}
		dep, err := r.ensureDeployment(ctx, &ta, cfg)
		if err != nil {
			_, failure := r.failStatus(ctx, &ta, orig, err)
			return failure
		}
		setAgentCond(&ta, CondConfigRendered, metav1.ConditionTrue, ReasonRendered, "secret and deployment rendered")
		rollupAgentStatus(&ta, cfg, dep)

		if err := r.writeStatus(ctx, &ta, orig); err != nil {
			return err
		}
		reconciled = &ta
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if missing {
		// Agent deleted: recompute shared node-reader subjects so its SA subject
		// is dropped (no finalizer — design §5.3).
		if _, err := r.reconcileNodeReaderSubjects(ctx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if reconciled != nil {
		log.Info("reconciled", "phase", reconciled.Status.Phase, "configHash", reconciled.Status.ObservedConfigHash)
	}
	return result, nil
}

func (r *TowonelAgentReconciler) checkAgentUnchanged(ctx context.Context, agent *towonelv1alpha1.TowonelAgent) error {
	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}
	var current towonelv1alpha1.TowonelAgent
	key := types.NamespacedName{Namespace: agent.Namespace, Name: agent.Name}
	if err := reader.Get(ctx, key, &current); err != nil {
		return fmt.Errorf("recheck agent %s: %w", key, err)
	}
	if current.UID != agent.UID || current.Generation != agent.Generation {
		return apierrors.NewConflict(towonelv1alpha1.GroupVersion.WithResource("towonelagents").GroupResource(), agent.Name, fmt.Errorf("agent changed during reconcile; recomputing"))
	}
	return nil
}

func (r *TowonelAgentReconciler) checkTunnelUnchanged(ctx context.Context, tunnel *towonelv1alpha1.TowonelTunnel) error {
	reader := client.Reader(r.Client)
	if r.APIReader != nil {
		reader = r.APIReader
	}
	var current towonelv1alpha1.TowonelTunnel
	key := types.NamespacedName{Namespace: tunnel.Namespace, Name: tunnel.Name}
	if err := reader.Get(ctx, key, &current); err != nil {
		return fmt.Errorf("recheck tunnel %s: %w", key, err)
	}
	if current.ResourceVersion != tunnel.ResourceVersion {
		return apierrors.NewConflict(towonelv1alpha1.GroupVersion.WithResource("towoneltunnels").GroupResource(), tunnel.Name, fmt.Errorf("tunnel changed during reconcile; recomputing"))
	}
	return nil
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
