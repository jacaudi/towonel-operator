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
	modeWrite   targetMode = iota // Managed mode: contribute routing
	modeObserve                   // ObserveOnly mode: validate only, never mutate
	modeSkip                      // an Event was emitted; do nothing
)

// errDefaultAgentNameClash: a non-operator-owned agent squats the default name.
var errDefaultAgentNameClash = errors.New("default-agent name occupied by a non-operator-owned agent")

func agentIsOperatorOwned(ta *towonelv1alpha1.TowonelAgent) bool {
	return ta.Labels[LabelManagedBy] == ManagedByValue
}

// agentMode returns the agent's management mode, treating an unset value as the
// Managed default. The CRD default applies server-side; this guards in code for
// objects observed before defaulting (the fake client in unit tests, or CRs
// authored before the field existed).
func agentMode(ta *towonelv1alpha1.TowonelAgent) towonelv1alpha1.AgentManagementMode {
	if ta.Spec.Mode == "" {
		return towonelv1alpha1.ModeManaged
	}
	return ta.Spec.Mode
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

// resolveTunnel resolves the tunnel a source targets (design §4). A non-empty
// tunnel-ref parses as "[<ns>/]<name>"; an empty ref defaults to the sole
// TowonelTunnel in the cluster if only one exists. Returns
// (nn, true, nil) on success, ("", false, nil) after emitting on a validation
// skip, or ("", false, err) on a transient List failure (requeue).
func resolveTunnel(ctx context.Context, c client.Client, emit func(reason, msg string), raw, srcNS string) (types.NamespacedName, bool, error) {
	if strings.TrimSpace(raw) != "" {
		nn, err := parseTunnelRef(raw, srcNS)
		if err != nil {
			emit(ReasonTunnelRefMissing, err.Error())
			return types.NamespacedName{}, false, nil
		}
		return nn, true, nil
	}
	var list towonelv1alpha1.TowonelTunnelList
	if err := c.List(ctx, &list); err != nil {
		return types.NamespacedName{}, false, err
	}
	if len(list.Items) != 1 {
		emit(ReasonTunnelRefMissing, fmt.Sprintf("no %s set and %d TowonelTunnels exist", AnnotationTunnelRef, len(list.Items)))
		return types.NamespacedName{}, false, nil
	}
	t := &list.Items[0]
	return types.NamespacedName{Namespace: t.Namespace, Name: t.Name}, true, nil
}

// agentNamespaceFor returns where the default agent lives: the configured
// --agent-namespace, else the tunnel's namespace (design §3.1).
func agentNamespaceFor(configured string, tunnel types.NamespacedName) string {
	if configured != "" {
		return configured
	}
	return tunnel.Namespace
}

// getDefaultAgent returns the operator-owned default agent for a tunnel when it
// already exists; (nil, nil) means its deterministic name is free. A
// non-operator-owned squatter at that name returns errDefaultAgentNameClash.
func getDefaultAgent(ctx context.Context, c client.Client, agentNS string, tunnel types.NamespacedName) (*towonelv1alpha1.TowonelAgent, error) {
	key := types.NamespacedName{Namespace: agentNS, Name: defaultAgentName(tunnel.Namespace, tunnel.Name)}
	var existing towonelv1alpha1.TowonelAgent
	switch err := c.Get(ctx, key, &existing); {
	case err == nil:
		if !agentIsOperatorOwned(&existing) {
			return nil, errDefaultAgentNameClash
		}
		return &existing, nil
	case apierrors.IsNotFound(err):
		return nil, nil
	default:
		return nil, err
	}
}

// createDefaultAgent mints the single operator-owned default agent for a tunnel
// (design §6). NEVER reached via agent-ref.
func createDefaultAgent(ctx context.Context, c client.Client, agentNS string, tunnel types.NamespacedName) (*towonelv1alpha1.TowonelAgent, error) {
	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{
			Name:        defaultAgentName(tunnel.Namespace, tunnel.Name),
			Namespace:   agentNS,
			Labels:      map[string]string{LabelManagedBy: ManagedByValue, LabelPartOf: PartOfValue},
			Annotations: map[string]string{AnnotationAutoCreated: "true"},
		},
		Spec: towonelv1alpha1.TowonelAgentSpec{
			Mode:      towonelv1alpha1.ModeManaged,
			TunnelRef: towonelv1alpha1.TunnelReference{Name: tunnel.Name, Namespace: tunnel.Namespace},
		},
	}
	switch err := c.Create(ctx, ta); {
	case err == nil:
		return ta, nil
	case apierrors.IsAlreadyExists(err):
		existing, gerr := getDefaultAgent(ctx, c, agentNS, tunnel)
		if gerr == nil && existing == nil {
			gerr = err
		}
		return existing, gerr
	default:
		return nil, err
	}
}

// soleAgent returns the only TowonelAgent in the cluster, 
// or nil when zero ormore than one exist.
func soleAgent(ctx context.Context, c client.Client) (*towonelv1alpha1.TowonelAgent, error) {
	var list towonelv1alpha1.TowonelAgentList
	if err := c.List(ctx, &list); err != nil {
		return nil, err
	}
	if len(list.Items) != 1 {
		return nil, nil
	}
	return &list.Items[0], nil
}

// resolveTarget applies the mode-based agent-ref policy (design §3.2). On
// modeSkip it has already emitted an Event via emit. 
// An empty agent-ref resolves the default target.
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
		return resolveDefaultTarget(ctx, c, emit, agentNS, tunnel)
	}
	var ta towonelv1alpha1.TowonelAgent
	if err := c.Get(ctx, types.NamespacedName{Namespace: agentNS, Name: agentRef}, &ta); err != nil {
		if apierrors.IsNotFound(err) {
			emit(ReasonAgentRefNotFound, fmt.Sprintf("agent-ref %q not found in namespace %q (agents are never auto-created via agent-ref)", agentRef, agentNS))
			return nil, modeSkip, nil
		}
		return nil, modeSkip, err
	}
	return classifyTarget(emit, &ta, tunnel)
}

// resolveDefaultTarget picks the agent for a source with no agent-ref: the
// existing operator-owned default, else the sole agent in a single-agent cluster
// (validated like an explicit ref), else a new operator-owned default.
func resolveDefaultTarget(ctx context.Context, c client.Client, emit func(reason, msg string), agentNS string, tunnel types.NamespacedName) (*towonelv1alpha1.TowonelAgent, targetMode, error) {
	switch def, err := getDefaultAgent(ctx, c, agentNS, tunnel); {
	case errors.Is(err, errDefaultAgentNameClash):
		emit(ReasonDefaultAgentClash, err.Error())
		return nil, modeSkip, nil
	case err != nil:
		return nil, modeSkip, err
	case def != nil:
		return def, modeWrite, nil
	}
	// The default name is free. Adopt the sole agent when exactly one exists
	//  so its routing needs no agent-ref; otherwise create the operator-owned default.
	sole, err := soleAgent(ctx, c)
	if err != nil {
		return nil, modeSkip, err
	}
	if sole != nil {
		return classifyTarget(emit, sole, tunnel)
	}
	ta, err := createDefaultAgent(ctx, c, agentNS, tunnel)
	if err != nil {
		return nil, modeSkip, err
	}
	return ta, modeWrite, nil
}

// classifyTarget decides how to treat a resolved agent (design §3.2): observe
// when ObserveOnly, write when Managed (default, incl. unset) and bound to this
// tunnel, skip (with an Event) when it is bound elsewhere. No ownership-label
// check — a resolved agent is reconciled by intent (issue #18); lifecycle stays
// user-owned.
func classifyTarget(emit func(reason, msg string), ta *towonelv1alpha1.TowonelAgent, tunnel types.NamespacedName) (*towonelv1alpha1.TowonelAgent, targetMode, error) {
	if agentMode(ta) == towonelv1alpha1.ModeObserveOnly {
		return ta, modeObserve, nil
	}
	if want := resolvedTunnelRef(ta); want != tunnel { // resolvedTunnelRef from indexes.go
		emit(ReasonAgentRefConflict, fmt.Sprintf("agent %q targets tunnel %s, not %s", ta.Name, want, tunnel))
		return nil, modeSkip, nil
	}
	return ta, modeWrite, nil
}
