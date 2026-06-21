package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// sourcePredicate returns a predicate that filters events to objects that carry
// (or carried) the towonel.io/tunnel annotation. This prevents the initial
// LIST of all Services (including kube-system) from flooding the reconcile
// queue and starving test-created Services under the race detector.
//
// Trade-off: if a source's towonel.io/tunnel annotation is removed while the
// operator is DOWN, the restart cache-list excludes that object, so its stale
// field-manager ownership on the agent is not released until the object is next
// touched (e.g. any edit). Impact is low — a single edit repairs it.
func sourcePredicate() predicate.Predicate {
	hasAnnotation := func(obj client.Object) bool {
		_, ok := obj.GetAnnotations()[AnnotationTunnel]
		return ok
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return hasAnnotation(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return hasAnnotation(e.ObjectOld) || hasAnnotation(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return hasAnnotation(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return hasAnnotation(e.Object) },
	}
}

// crossWatchPredicate filters the secondary (cross-resource) watches — the
// parent-Gateway watch and the TowonelAgent watch — so a status-only update does
// not re-enqueue every dependent source on every status write (churn), while
// still firing on Create and on any spec, annotation, or label change.
//
// AnnotationChangedPredicate is REQUIRED, not optional: a Gateway's
// towonel.io/gateway-service is an ANNOTATION (httproute_source_controller.go:106),
// and annotation edits do NOT bump metadata.generation — so GenerationChanged
// alone would silently drop a gateway-service edit. LabelChangedPredicate matters
// for the TowonelAgent watch: resolveTarget keys modeWrite/modeObserve on the
// agent's managed-by LABEL (agentIsOperatorOwned), which can flip with no
// generation bump. Each predicate overrides only Update; Create defaults to true,
// so the #22 create scenario always passes.
func crossWatchPredicate() predicate.Predicate {
	return predicate.Or(
		predicate.GenerationChangedPredicate{},
		predicate.AnnotationChangedPredicate{},
		predicate.LabelChangedPredicate{},
	)
}

// sourceBase carries the shared recorder/dedupe + the contribute orchestration
// used by all three source controllers. Embedded by value; lazy-init via once.
type sourceBase struct {
	recorder *SafeRecorder
	dedupe   *eventDedupe
	once     sync.Once
}

func (b *sourceBase) ensure(rec record.EventRecorder) {
	b.once.Do(func() {
		if b.recorder == nil {
			b.recorder = NewSafeRecorder(rec)
		}
		if b.dedupe == nil {
			b.dedupe = newEventDedupe()
		}
	})
}

// applyContribution runs the shared write/observe/release/GC/advisory flow once a
// controller has parsed opt-in + tunnel-ref + agent-ref + derived routing.
func (b *sourceBase) applyContribution(
	ctx context.Context,
	c client.Client,
	agentNSConfig, kind string,
	src client.Object,
	tunnel types.NamespacedName,
	agentRef string,
	rt routing,
) (reconcile.Result, error) {
	fieldMgr := srcFieldManager(kind, src.GetNamespace(), src.GetName())
	emit := func(reason, msg string) { b.dedupe.emit(b.recorder, src, corev1.EventTypeWarning, reason, msg) }

	target, mode, err := resolveTarget(ctx, c, emit, agentNSConfig, tunnel, agentRef)
	if err != nil {
		return reconcile.Result{}, err
	}
	switch mode {
	case modeSkip:
		return reconcile.Result{}, releaseFromOtherAgents(ctx, c, fieldMgr, nil)
	case modeObserve:
		b.observeUserAgent(emit, target, tunnel, rt)
		return reconcile.Result{}, releaseFromOtherAgents(ctx, c, fieldMgr, nil)
	}

	// modeWrite.
	targetNN := types.NamespacedName{Namespace: target.Namespace, Name: target.Name}
	if err := contributeRouting(ctx, c, targetNN, fieldMgr, rt); err != nil {
		if errors.Is(err, errHostnameConflict) {
			emit(ReasonHostnameConflict, err.Error())
			return reconcile.Result{}, nil // not retryable without a user edit
		}
		return reconcile.Result{}, err
	}
	// STRICTLY AFTER the successful apply:
	if err := releaseFromOtherAgents(ctx, c, fieldMgr, &targetNN); err != nil {
		return reconcile.Result{}, err
	}
	// Inform (once per TTL) when the operator reconciles routing into an agent it
	// does not own the lifecycle of — a hand-authored Managed agent. Routing only;
	// the agent's lifecycle stays the user's.
	if !agentIsOperatorOwned(target) {
		b.dedupe.emit(b.recorder, src, corev1.EventTypeNormal, ReasonReconcilingAgent,
			fmt.Sprintf("operator is reconciling routing into hand-authored agent %s/%s (effective spec.mode: Managed); it manages routing only, never this agent's lifecycle", target.Namespace, target.Name))
	}
	b.adviseIfMultipleAgents(ctx, c, emit, tunnel, targetNN)
	// No orphan-GC on this path. The contribute just made the agent non-empty,
	// so it can never legitimately be empty here — and re-reading the manager's
	// CACHED client immediately after the SSA write can return a stale,
	// pre-write view (empty services, source field-manager not yet visible),
	// which would wrongly DELETE the freshly-contributed agent. Orphan-GC of an
	// emptied auto-created agent runs only on the opt-out/delete path
	// (releaseEverywhere), where the agent genuinely becomes empty.
	return reconcile.Result{}, nil
}

// releaseResult converts a releaseEverywhere error into the appropriate
// reconcile result: requeue after waitingRequeue when GC is in-flight,
// otherwise propagate the error (or nil).
func releaseResult(err error) (reconcile.Result, error) {
	if errors.Is(err, errOrphanInFlight) {
		return reconcile.Result{RequeueAfter: waitingRequeue}, nil
	}
	return reconcile.Result{}, err
}

// releaseEverywhere drops this source's ownership cluster-wide and GCs any
// now-empty auto-created agent. Used on opt-out / object deletion.
// apiReader is an uncached API reader (mgr.GetAPIReader()) used exclusively for
// the GC-decision Get inside orphanGCIfEmpty — the cached client lags SSA
// writes and would skip the delete on a stale view.
// Returns errOrphanInFlight when a GC candidate exists but a source-manager
// apply is still in flight (cache hasn't reflected the release yet); callers
// should requeue so the GC can be retried on the next reconcile.
func (b *sourceBase) releaseEverywhere(ctx context.Context, apiReader client.Reader, c client.Client, kind, srcNS, srcName string) error {
	fieldMgr := srcFieldManager(kind, srcNS, srcName)
	if err := releaseFromOtherAgents(ctx, c, fieldMgr, nil); err != nil {
		return err
	}
	// GC candidates are only ever auto-created agents, which always carry the
	// managed-by label (both stamped together in ensureDefaultAgent), so this
	// filter never skips a deletable agent. Cluster-wide field-manager release for
	// unlabeled hand-authored agents already happened in releaseFromOtherAgents above.
	var list towonelv1alpha1.TowonelAgentList
	if err := c.List(ctx, &list, client.MatchingLabels{LabelManagedBy: ManagedByValue}); err != nil {
		return err
	}
	inFlight := false
	for i := range list.Items {
		nn := types.NamespacedName{Namespace: list.Items[i].Namespace, Name: list.Items[i].Name}
		if err := orphanGCIfEmpty(ctx, apiReader, c, nn); err != nil {
			if errors.Is(err, errOrphanInFlight) {
				inFlight = true // retry on next reconcile
				continue
			}
			return err
		}
	}
	if inFlight {
		return errOrphanInFlight
	}
	return nil
}

// observeUserAgent validates a user-owned agent-ref target WITHOUT mutating it
// (design §3.2): wrong tunnel, or hostnames the user agent does not serve.
func (b *sourceBase) observeUserAgent(emit func(reason, msg string), target *towonelv1alpha1.TowonelAgent, tunnel types.NamespacedName, rt routing) {
	if resolvedTunnelRef(target) != tunnel {
		emit(ReasonObserveOnly, fmt.Sprintf("agent-ref %q references tunnel %s, not %s; operator is observe-only and will not modify it", target.Name, resolvedTunnelRef(target), tunnel))
		return
	}
	served := make(map[string]bool, len(target.Spec.Services))
	for _, s := range target.Spec.Services {
		served[s.Hostname] = true
	}
	for _, s := range rt.services {
		if h, _ := s["hostname"].(string); h != "" && !served[h] {
			emit(ReasonObserveOnly, fmt.Sprintf("hostname %q is annotated here but not served by agent %q, which is in observe-only mode; set its spec.mode: Managed to let the operator manage routing", h, target.Name))
		}
	}
}

// adviseIfMultipleAgents warns when more than one TowonelAgent references the
// tunnel — divergent hostname sets clobber at the hub (design §3.2). Advisory.
func (b *sourceBase) adviseIfMultipleAgents(ctx context.Context, c client.Client, emit func(reason, msg string), tunnel, targetNN types.NamespacedName) {
	var list towonelv1alpha1.TowonelAgentList
	if err := c.List(ctx, &list); err != nil {
		return
	}
	for i := range list.Items {
		a := &list.Items[i]
		nn := types.NamespacedName{Namespace: a.Namespace, Name: a.Name}
		if nn != targetNN && resolvedTunnelRef(a) == tunnel {
			emit(ReasonMultipleAgents, "multiple TowonelAgents reference this tunnel; Towonel routing is tenant-global and divergent hostname sets will clobber at the hub — consolidate to one agent")
			return
		}
	}
}
