package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// targetMode is the operator's interaction with a resolved agent (design §3.2).
type targetMode int

const (
	modeWrite   targetMode = iota // operator-owned: contribute routing
	modeObserve                   // user-owned (agent-ref): validate only, never mutate
	modeSkip                      // an Event was emitted; do nothing
)

// errDefaultAgentNameClash: a non-operator-owned agent squats the default name.
var errDefaultAgentNameClash = errors.New("default-agent name occupied by a non-operator-owned agent")

func agentIsOperatorOwned(ta *towonelv1alpha1.TowonelAgent) bool {
	return ta.Labels[LabelManagedBy] == ManagedByValue
}

// parseTunnelRef parses "[<ns>/]<name>"; a bare name resolves in srcNS.
func parseTunnelRef(raw, srcNS string) (types.NamespacedName, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return types.NamespacedName{}, fmt.Errorf("empty tunnel-ref")
	}
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		ns, name := raw[:i], raw[i+1:]
		if ns == "" || name == "" {
			return types.NamespacedName{}, fmt.Errorf("malformed tunnel-ref %q", raw)
		}
		return types.NamespacedName{Namespace: ns, Name: name}, nil
	}
	return types.NamespacedName{Namespace: srcNS, Name: raw}, nil
}

// agentNamespaceFor returns where the default agent lives: the configured
// --agent-namespace, else the tunnel's namespace (design §3.1).
func agentNamespaceFor(configured string, tunnel types.NamespacedName) string {
	if configured != "" {
		return configured
	}
	return tunnel.Namespace
}

// sourceTargetsAgent reports whether a source carrying the given annotations and
// namespace would, on reconcile, contribute routing to agent ta. It mirrors the
// explicit agent-ref branch of resolveTarget: the source must name ta via
// towonel.io/agent-ref AND ta must live in the resolved agent namespace
// (agentNamespaceFor(config, tunnel)). Used by the TowonelAgent->source watch to
// re-enqueue sources stranded when their agent-ref agent did not yet exist (#22).
//
// Scope: only EXPLICIT agent-ref sources are matched. The default-agent path
// (no agent-ref) cannot strand under #22 — resolveTarget create-or-gets the
// default agent and contributeRouting populates it within the same reconcile
// (see ensureDefaultAgent + applyContribution) — so an agent event never needs
// to re-enqueue an agent-ref-less source, and matching them would over-enqueue.
func sourceTargetsAgent(ann map[string]string, srcNS, agentNSConfig string, ta *towonelv1alpha1.TowonelAgent) bool {
	agentRef := ann[AnnotationAgentRef]
	if agentRef == "" || agentRef != ta.Name {
		return false
	}
	tunnel, err := parseTunnelRef(ann[AnnotationTunnelRef], srcNS)
	if err != nil {
		return false
	}
	return agentNamespaceFor(agentNSConfig, tunnel) == ta.Namespace
}

// ensureDefaultAgent create-or-gets the single operator-owned default agent for
// a tunnel (design §6). NEVER reached via agent-ref.
func ensureDefaultAgent(ctx context.Context, c client.Client, agentNS string, tunnel types.NamespacedName) (*towonelv1alpha1.TowonelAgent, error) {
	name := defaultAgentName(tunnel.Namespace, tunnel.Name)
	key := types.NamespacedName{Namespace: agentNS, Name: name}
	var existing towonelv1alpha1.TowonelAgent
	switch err := c.Get(ctx, key, &existing); {
	case err == nil:
		if !agentIsOperatorOwned(&existing) {
			return nil, errDefaultAgentNameClash
		}
		return &existing, nil
	case !apierrors.IsNotFound(err):
		return nil, err
	}
	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   agentNS,
			Labels:      map[string]string{LabelManagedBy: ManagedByValue, LabelPartOf: PartOfValue},
			Annotations: map[string]string{AnnotationAutoCreated: "true"},
		},
		Spec: towonelv1alpha1.TowonelAgentSpec{
			TunnelRef: towonelv1alpha1.TunnelReference{Name: tunnel.Name, Namespace: tunnel.Namespace},
		},
	}
	if err := c.Create(ctx, ta); err != nil {
		if apierrors.IsAlreadyExists(err) { // lost the create race
			if gerr := c.Get(ctx, key, &existing); gerr != nil {
				return nil, gerr
			}
			if !agentIsOperatorOwned(&existing) {
				return nil, errDefaultAgentNameClash
			}
			return &existing, nil
		}
		return nil, err
	}
	return ta, nil
}

// resolveTarget applies the ownership-based agent-ref policy (design §3.2). On
// modeSkip it has already emitted an Event via emit.
func resolveTarget(
	ctx context.Context,
	c client.Client,
	emit func(reason, msg string),
	agentNSConfig string,
	tunnel types.NamespacedName,
	agentRef string,
) (*towonelv1alpha1.TowonelAgent, targetMode, error) {
	agentNS := agentNamespaceFor(agentNSConfig, tunnel)
	if agentRef == "" {
		ta, err := ensureDefaultAgent(ctx, c, agentNS, tunnel)
		if err != nil {
			if errors.Is(err, errDefaultAgentNameClash) {
				emit(ReasonDefaultAgentClash, err.Error())
				return nil, modeSkip, nil
			}
			return nil, modeSkip, err
		}
		return ta, modeWrite, nil
	}
	var ta towonelv1alpha1.TowonelAgent
	if err := c.Get(ctx, types.NamespacedName{Namespace: agentNS, Name: agentRef}, &ta); err != nil {
		if apierrors.IsNotFound(err) {
			emit(ReasonAgentRefNotFound, fmt.Sprintf("agent-ref %q not found in namespace %q (agents are never auto-created via agent-ref)", agentRef, agentNS))
			return nil, modeSkip, nil
		}
		return nil, modeSkip, err
	}
	if !agentIsOperatorOwned(&ta) {
		return &ta, modeObserve, nil
	}
	if want := resolvedTunnelRef(&ta); want != tunnel { // resolvedTunnelRef from indexes.go
		emit(ReasonAgentRefConflict, fmt.Sprintf("agent-ref %q targets tunnel %s, not %s", agentRef, want, tunnel))
		return nil, modeSkip, nil
	}
	return &ta, modeWrite, nil
}
