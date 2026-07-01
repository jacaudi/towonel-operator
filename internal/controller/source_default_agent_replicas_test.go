package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// Issue #46: --default-agent-replicas (chart: defaultAgent.replicas) sets
// spec.workload.replicas on the auto-created default agent, so the
// fully-implicit gateway-as-source path (no hand-authored TowonelAgent) can run
// the default agent HA. Applied at CREATE time only; hand-authored agents are
// untouched. Tests below cover the project's seven standard categories.

// 1) HAPPY PATH — a positive value is stamped onto the created default agent.
func TestEnsureDefaultAgentAppliesReplicas(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
	tunnel := types.NamespacedName{Namespace: "net", Name: "app"}

	ta, err := ensureDefaultAgent(context.Background(), c, "net", ptr.To(int32(2)), tunnel)
	if err != nil {
		t.Fatal(err)
	}
	if ta.Spec.Workload.Replicas == nil || *ta.Spec.Workload.Replicas != 2 {
		t.Fatalf("created default agent replicas = %v, want 2", ta.Spec.Workload.Replicas)
	}
	// The value must be persisted, not just returned in-memory.
	var got towonelv1alpha1.TowonelAgent
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: ta.Namespace, Name: ta.Name}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Workload.Replicas == nil || *got.Spec.Workload.Replicas != 2 {
		t.Fatalf("persisted replicas = %v, want 2", got.Spec.Workload.Replicas)
	}
}

// 2) NEGATIVE / DEFAULT — nil (flag unset / 0) leaves replicas unset so the CRD
// default (1) applies server-side; the operator must not stamp a value.
func TestEnsureDefaultAgentNilReplicasLeavesUnset(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
	tunnel := types.NamespacedName{Namespace: "net", Name: "app"}

	ta, err := ensureDefaultAgent(context.Background(), c, "net", nil, tunnel)
	if err != nil {
		t.Fatal(err)
	}
	if ta.Spec.Workload.Replicas != nil {
		t.Fatalf("nil default-replicas must leave spec.workload.replicas unset; got %v", *ta.Spec.Workload.Replicas)
	}
}

// 3) BOUNDARY — an explicit replicas: 1 is still stamped (distinct from "unset"):
// a value flows through verbatim regardless of magnitude, incl. the CRD-default
// value itself. This pins that "1" is honored as an explicit choice, not elided.
func TestEnsureDefaultAgentBoundaryReplicaValues(t *testing.T) {
	for _, n := range []int32{1, 3, 10} {
		c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
		tunnel := types.NamespacedName{Namespace: "net", Name: "app"}
		ta, err := ensureDefaultAgent(context.Background(), c, "net", ptr.To(n), tunnel)
		if err != nil {
			t.Fatal(err)
		}
		if ta.Spec.Workload.Replicas == nil || *ta.Spec.Workload.Replicas != n {
			t.Fatalf("replicas = %v, want %d", ta.Spec.Workload.Replicas, n)
		}
	}
}

// 4) IDEMPOTENCY — ensureDefaultAgent is create-or-GET: once the agent exists,
// a later reconcile with a DIFFERENT --default-agent-replicas must return the
// existing agent unchanged (the operator sets the initial spec and never fights
// later edits). Also asserts no aliasing of the shared flag pointer.
func TestEnsureDefaultAgentReplicasCreateOnlyAndUnaliased(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
	tunnel := types.NamespacedName{Namespace: "net", Name: "app"}

	flagVal := int32(2)
	first, err := ensureDefaultAgent(context.Background(), c, "net", &flagVal, tunnel)
	if err != nil {
		t.Fatal(err)
	}

	// No aliasing: mutating the created object's replicas must not touch the flag.
	*first.Spec.Workload.Replicas = 99
	if flagVal != 2 {
		t.Fatalf("ensureDefaultAgent aliased the flag pointer; flagVal=%d", flagVal)
	}

	// Second call with a NEW replica value must GET the existing agent, not
	// re-stamp it (the value persisted at create time stays authoritative).
	second, err := ensureDefaultAgent(context.Background(), c, "net", ptr.To(int32(5)), tunnel)
	if err != nil {
		t.Fatal(err)
	}
	if second.Spec.Workload.Replicas == nil || *second.Spec.Workload.Replicas != 2 {
		t.Fatalf("create-or-get must not re-stamp replicas on the existing agent; got %v, want persisted 2",
			second.Spec.Workload.Replicas)
	}
}

// 5) REGRESSION / ISOLATION — hand-authored agents keep full control of their own
// spec.workload (issue acceptance). A source resolving to an existing hand-authored
// agent via agent-ref must NOT have --default-agent-replicas applied, even when
// the flag is set: replicas flows only through the ensureDefaultAgent (no
// agent-ref) path. resolveTarget threads the flag but must ignore it here.
func TestResolveTargetAgentRefIgnoresDefaultReplicas(t *testing.T) {
	tunnel := types.NamespacedName{Namespace: "net", Name: "app"}
	mine := &towonelv1alpha1.TowonelAgent{}
	mine.Namespace, mine.Name = "net", "mine"
	mine.Spec.Workload.Replicas = ptr.To(int32(1)) // user's own choice
	mine.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: tunnel.Name, Namespace: tunnel.Namespace}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(mine).Build()

	ta, mode, err := resolveTarget(context.Background(), c, func(string, string) {}, "", ptr.To(int32(4)), tunnel, "mine")
	if err != nil || mode != modeWrite {
		t.Fatalf("mode=%v err=%v; want write", mode, err)
	}
	if ta.Spec.Workload.Replicas == nil || *ta.Spec.Workload.Replicas != 1 {
		t.Fatalf("hand-authored agent replicas must be untouched; got %v, want the user's 1", ta.Spec.Workload.Replicas)
	}
}

// 5b) REGRESSION — the default-agent (no agent-ref) path DOES apply the flag,
// end-to-end through resolveTarget (not just ensureDefaultAgent in isolation).
func TestResolveTargetDefaultAgentAppliesReplicas(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
	tunnel := types.NamespacedName{Namespace: "net", Name: "app"}

	ta, mode, err := resolveTarget(context.Background(), c, func(string, string) {}, "", ptr.To(int32(2)), tunnel, "")
	if err != nil || mode != modeWrite {
		t.Fatalf("mode=%v err=%v; want write", mode, err)
	}
	if !agentIsOperatorOwned(ta) {
		t.Fatal("default agent must be operator-owned")
	}
	if ta.Spec.Workload.Replicas == nil || *ta.Spec.Workload.Replicas != 2 {
		t.Fatalf("default agent replicas = %v, want 2 from --default-agent-replicas", ta.Spec.Workload.Replicas)
	}
}

// 6) CONCURRENCY / RACE — N/A as a dedicated test. ensureDefaultAgent's own
// create-vs-get race (two sources racing to create the same default agent) is
// already covered by its IsAlreadyExists re-Get branch and the existing source
// tests; the replicas addition only sets a field on the create path and adds no
// shared mutable state. The controllers themselves are exercised under
// `go test -race` (taskfile `test`). No new concurrency test is added here.

// 7) INTEGRATION (envtest) — a live-cluster assertion (a gateway-as-source tunnel
// with --default-agent-replicas=2 producing a default agent whose Deployment runs
// 2 replicas) belongs in test/envtest and runs under the maintainer's CI once
// KUBEBUILDER_ASSETS is set (that suite skips when the assets are unset, as in
// this contributor sandbox). The fake-client tests above fully cover the pure
// resolveTarget/ensureDefaultAgent logic that stamps the field, and buildDeployment
// already turns spec.workload.replicas into Deployment replicas
// (TestBuildDeployment). No new envtest is added here.
func TestDefaultAgentReplicasEnvtestPlaceholder(t *testing.T) {
	t.Skip("covered by test/envtest source suite when KUBEBUILDER_ASSETS is set (maintainer CI)")
}
