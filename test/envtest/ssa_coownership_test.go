package envtest_test

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

var agentGVK = schema.GroupVersionKind{Group: "towonel.io", Version: "v1alpha1", Kind: "TowonelAgent"}

// applyServices server-side-applies ONLY spec.services for the given hostnames
// under fieldMgr, with no force. Mirrors how a source controller contributes.
func applyServices(ctx context.Context, c client.Client, nn types.NamespacedName, fieldMgr string, hostnames ...string) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(agentGVK)
	u.SetNamespace(nn.Namespace)
	u.SetName(nn.Name)
	svcs := make([]any, 0, len(hostnames))
	for _, h := range hostnames {
		svcs = append(svcs, map[string]any{"hostname": h, "origin": h + ":8080"})
	}
	if err := unstructured.SetNestedSlice(u.Object, svcs, "spec", "services"); err != nil {
		return err
	}
	return c.Patch(ctx, u, client.Apply, client.FieldOwner(fieldMgr))
}

// releaseServices applies a spec-less object under fieldMgr, releasing all of
// that manager's owned spec fields.
func releaseServices(ctx context.Context, c client.Client, nn types.NamespacedName, fieldMgr string) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(agentGVK)
	u.SetNamespace(nn.Namespace)
	u.SetName(nn.Name)
	return c.Patch(ctx, u, client.Apply, client.FieldOwner(fieldMgr))
}

func hostSet(ta *towonelv1alpha1.TowonelAgent) map[string]bool {
	out := map[string]bool{}
	for _, s := range ta.Spec.Services {
		out[s.Hostname] = true
	}
	return out
}

func TestSSACoOwnershipOfServices(t *testing.T) {
	ctx := context.Background()
	ns := mustNamespace(t)
	c := k8sClient

	// Ensure-path: create the agent shell with tunnelRef (typed) under the
	// operator field manager. tunnelRef must NOT be touched by source managers.
	agent := &towonelv1alpha1.TowonelAgent{}
	agent.Namespace = ns
	agent.Name = "shared"
	agent.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "t", Namespace: ns}
	if err := c.Create(ctx, agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	nn := types.NamespacedName{Namespace: ns, Name: "shared"}

	// Two managers contribute disjoint hostnames.
	if err := applyServices(ctx, c, nn, "towonel-src:Service:ns:a", "a.example"); err != nil {
		t.Fatalf("apply A: %v", err)
	}
	if err := applyServices(ctx, c, nn, "towonel-src:Service:ns:b", "b.example"); err != nil {
		t.Fatalf("apply B: %v", err)
	}
	var got towonelv1alpha1.TowonelAgent
	if err := c.Get(ctx, nn, &got); err != nil {
		t.Fatal(err)
	}
	hs := hostSet(&got)
	if !hs["a.example"] || !hs["b.example"] {
		t.Fatalf("co-ownership failed: %v", hs)
	}
	if got.Spec.TunnelRef.Name != "t" {
		t.Fatalf("source apply clobbered tunnelRef: %q", got.Spec.TunnelRef.Name)
	}

	// Shrink-prune: A re-applies a different hostname; its old entry is pruned.
	if err := applyServices(ctx, c, nn, "towonel-src:Service:ns:a", "a2.example"); err != nil {
		t.Fatalf("re-apply A: %v", err)
	}
	if err := c.Get(ctx, nn, &got); err != nil {
		t.Fatal(err)
	}
	hs = hostSet(&got)
	if hs["a.example"] || !hs["a2.example"] || !hs["b.example"] {
		t.Fatalf("shrink-prune failed: %v", hs)
	}

	// Empty-apply release: A releases; only B's entry remains.
	if err := releaseServices(ctx, c, nn, "towonel-src:Service:ns:a"); err != nil {
		t.Fatalf("release A: %v", err)
	}
	if err := c.Get(ctx, nn, &got); err != nil {
		t.Fatal(err)
	}
	hs = hostSet(&got)
	if hs["a2.example"] || !hs["b.example"] {
		t.Fatalf("release failed: %v", hs)
	}
}
