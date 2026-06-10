package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// agentTunnelRefIndex indexes TowonelAgents by their resolved tunnelRef key
// ("<ns>/<name>"). Consumed by BOTH controllers, so registration lives here —
// called once from main.go and once per envtest manager (design §3.3).
const agentTunnelRefIndex = "spec.tunnelRef"

// resolvedTunnelRef returns the agent's tunnelRef with the namespace defaulted
// to the agent's own.
func resolvedTunnelRef(ta *towonelv1alpha1.TowonelAgent) types.NamespacedName {
	ns := ta.Spec.TunnelRef.Namespace
	if ns == "" {
		ns = ta.Namespace
	}
	return types.NamespacedName{Namespace: ns, Name: ta.Spec.TunnelRef.Name}
}

// RegisterIndexes installs the shared field indexes on the manager's cache.
func RegisterIndexes(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(ctx, &towonelv1alpha1.TowonelAgent{}, agentTunnelRefIndex,
		func(obj client.Object) []string {
			ta := obj.(*towonelv1alpha1.TowonelAgent)
			if ta.Spec.TunnelRef.Name == "" {
				return nil
			}
			return []string{resolvedTunnelRef(ta).String()}
		})
}
