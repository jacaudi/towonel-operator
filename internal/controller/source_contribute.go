package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

var agentApplyGVK = schema.GroupVersionKind{Group: "towonel.io", Version: "v1alpha1", Kind: "TowonelAgent"}

// errHostnameConflict signals an SSA field-manager conflict — another source
// already owns a routing key this source tried to set.
var errHostnameConflict = errors.New("routing entry already owned by another source")

// errOrphanInFlight: the auto-created agent is empty but a source manager still
// owns fields (apply in flight) — caller should requeue, not delete.
var errOrphanInFlight = errors.New("auto-created agent empty but a source apply is in flight")

// managedObject is the minimal interface ownsAnyField/hasSourceManager need.
type managedObject interface {
	GetManagedFields() []metav1.ManagedFieldsEntry
}

// routing is one source's desired contribution to an agent's spec. Map keys are
// CRD JSON field names (camelCase: edgeTLSMode/proxyProtocol/preferredPort) — NOT the
// agent's env snake_case. A wrong key is pruned silently by the apiserver.
type routing struct {
	services []map[string]any // {hostname, origin, edgeTLSMode?, proxyProtocol?}
	tcp      []map[string]any // {name, origin, preferredPort?}
	udp      []map[string]any
}

func (rt routing) empty() bool {
	return len(rt.services) == 0 && len(rt.tcp) == 0 && len(rt.udp) == 0
}

func toSlice(ms []map[string]any) []any {
	out := make([]any, len(ms))
	for i := range ms {
		out[i] = ms[i]
	}
	return out
}

func newAgentApply(nn types.NamespacedName) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(agentApplyGVK)
	u.SetNamespace(nn.Namespace)
	u.SetName(nn.Name)
	return u
}

// contributeRouting applies a source's routing into the agent under its
// per-source field manager (unstructured, NO force). Conflict -> errHostnameConflict.
func contributeRouting(ctx context.Context, c client.Client, nn types.NamespacedName, fieldMgr string, rt routing) error {
	u := newAgentApply(nn)
	spec := map[string]any{}
	if len(rt.services) > 0 {
		spec["services"] = toSlice(rt.services)
	}
	if len(rt.tcp) > 0 {
		spec["tcp"] = toSlice(rt.tcp)
	}
	if len(rt.udp) > 0 {
		spec["udp"] = toSlice(rt.udp)
	}
	if len(spec) > 0 {
		u.Object["spec"] = spec
	}
	if err := c.Patch(ctx, u, client.Apply, client.FieldOwner(fieldMgr)); err != nil {
		if apierrors.IsConflict(err) {
			return fmt.Errorf("%w: %v", errHostnameConflict, err)
		}
		return fmt.Errorf("apply routing to %s as %s: %w", nn, fieldMgr, err)
	}
	return nil
}

// releaseRouting applies a spec-less object under fieldMgr, releasing all of
// that manager's owned spec fields. NotFound (agent gone) is a no-op.
func releaseRouting(ctx context.Context, c client.Client, nn types.NamespacedName, fieldMgr string) error {
	if err := c.Patch(ctx, newAgentApply(nn), client.Apply, client.FieldOwner(fieldMgr)); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("release %s on %s: %w", fieldMgr, nn, err)
	}
	return nil
}

// releaseFromOtherAgents releases fieldMgr from every operator-managed agent
// CLUSTER-WIDE except keep. Durable retarget/opt-out cleanup via managedFields
// (design §5); cluster-wide so a cross-namespace tunnel's agent is reachable
// even without a tunnel hint.
func releaseFromOtherAgents(ctx context.Context, c client.Client, fieldMgr string, keep *types.NamespacedName) error {
	// List ALL agents, not just managed-by-labeled ones: a hand-authored Managed
	// agent the operator reconciles routing into carries no managed-by label, yet
	// this source may own routing fields on it that must be released on
	// retarget/opt-out. ownsAnyField below scopes the mutation to agents this
	// source actually owns a field on, so the broader List is safe.
	var list towonelv1alpha1.TowonelAgentList
	if err := c.List(ctx, &list); err != nil {
		return fmt.Errorf("list agents: %w", err)
	}
	for i := range list.Items {
		a := &list.Items[i]
		nn := types.NamespacedName{Namespace: a.Namespace, Name: a.Name}
		if keep != nil && nn == *keep {
			continue
		}
		if !ownsAnyField(a, fieldMgr) {
			continue
		}
		if err := releaseRouting(ctx, c, nn, fieldMgr); err != nil {
			return err
		}
	}
	return nil
}

func ownsAnyField(obj managedObject, fieldMgr string) bool {
	for _, mf := range obj.GetManagedFields() {
		if mf.Manager == fieldMgr && mf.Operation == metav1.ManagedFieldsOperationApply {
			return true
		}
	}
	return false
}

func hasSourceManager(obj managedObject) bool {
	for _, mf := range obj.GetManagedFields() {
		if mf.Operation == metav1.ManagedFieldsOperationApply && strings.HasPrefix(mf.Manager, "towonel-src:") {
			return true
		}
	}
	return false
}

// orphanGCIfEmpty deletes an auto-created agent with no routing AND no source
// manager. The GC-decision Get uses r (an uncached API reader) so the decision
// is authoritative — the cached client lags SSA writes by some milliseconds and
// can return a stale, pre-release view that skips the delete. The actual Delete
// stays on c (the normal cached client writer).
func orphanGCIfEmpty(ctx context.Context, r client.Reader, c client.Client, nn types.NamespacedName) error {
	var ta towonelv1alpha1.TowonelAgent
	if err := r.Get(ctx, nn, &ta); err != nil {
		return client.IgnoreNotFound(err)
	}
	if ta.Annotations[AnnotationAutoCreated] != "true" {
		return nil
	}
	if len(ta.Spec.Services) > 0 || len(ta.Spec.TCP) > 0 || len(ta.Spec.UDP) > 0 {
		return nil
	}
	if hasSourceManager(&ta) {
		return errOrphanInFlight
	}
	err := c.Delete(ctx, &ta, client.Preconditions{ResourceVersion: ptr.To(ta.ResourceVersion)})
	if apierrors.IsNotFound(err) || apierrors.IsConflict(err) {
		return nil // object gone or changed since the authoritative read — no-op
	}
	return err
}
