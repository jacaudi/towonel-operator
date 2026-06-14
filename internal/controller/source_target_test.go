package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func srcScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := towonelv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	_ = corev1.AddToScheme(s)
	_ = gwv1.Install(s)
	return s
}

func TestDefaultAgentNameCollisionSafe(t *testing.T) {
	if defaultAgentName("a", "b-c") == defaultAgentName("a-b", "c") {
		t.Fatal("sanitization collision: distinct tunnels share a default-agent name")
	}
	if defaultAgentName("net", "app") != defaultAgentName("net", "app") {
		t.Fatal("not deterministic")
	}
	if got := defaultAgentName("net", "app"); len(got) > 63 || got == "" {
		t.Fatalf("bad name %q", got)
	}
}

func TestParseTunnelRef(t *testing.T) {
	cases := []struct {
		in, srcNS, wantNS, wantName string
		wantErr                     bool
	}{
		{"app", "src", "src", "app", false},
		{"net/app", "src", "net", "app", false},
		{"", "src", "", "", true},
		{"net/", "src", "", "", true},
	}
	for _, c := range cases {
		got, err := parseTunnelRef(c.in, c.srcNS)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseTunnelRef(%q) want err", c.in)
			}
			continue
		}
		if err != nil || got.Namespace != c.wantNS || got.Name != c.wantName {
			t.Errorf("parseTunnelRef(%q) = %v,%v", c.in, got, err)
		}
	}
}

func TestResolveTargetDefaultCreatesOperatorOwned(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
	tunnel := types.NamespacedName{Namespace: "net", Name: "app"}
	ta, mode, err := resolveTarget(context.Background(), c, func(string, string) {}, "", tunnel, "")
	if err != nil || mode != modeWrite {
		t.Fatalf("mode=%v err=%v", mode, err)
	}
	if !agentIsOperatorOwned(ta) || ta.Annotations[AnnotationAutoCreated] != "true" {
		t.Fatalf("agent not stamped operator-owned/auto-created: %+v", ta.Labels)
	}
	if ta.Namespace != "net" || ta.Name != defaultAgentName("net", "app") {
		t.Fatalf("unexpected placement %s/%s", ta.Namespace, ta.Name)
	}
}

func TestResolveTargetAgentRefUserOwnedObserveOnly(t *testing.T) {
	mine := &towonelv1alpha1.TowonelAgent{}
	mine.Namespace, mine.Name = "net", "mine"
	mine.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: "net"}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(mine).Build()
	_, mode, err := resolveTarget(context.Background(), c, func(string, string) {}, "", types.NamespacedName{Namespace: "net", Name: "app"}, "mine")
	if err != nil || mode != modeObserve {
		t.Fatalf("mode=%v err=%v; want observe", mode, err)
	}
}

func TestResolveTargetAgentRefNotFoundSkips(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
	var reason string
	_, mode, err := resolveTarget(context.Background(), c, func(r, _ string) { reason = r }, "", types.NamespacedName{Namespace: "net", Name: "app"}, "ghost")
	if err != nil || mode != modeSkip || reason != ReasonAgentRefNotFound {
		t.Fatalf("mode=%v reason=%q err=%v", mode, reason, err)
	}
	// And it must NOT have auto-created anything.
	var list towonelv1alpha1.TowonelAgentList
	_ = c.List(context.Background(), &list)
	if len(list.Items) != 0 {
		t.Fatalf("agent-ref must never auto-create; found %d", len(list.Items))
	}
}

func TestResolveTargetDefaultSquatSkips(t *testing.T) {
	tunnel := types.NamespacedName{Namespace: "net", Name: "app"}
	// A user-owned agent squats the deterministic default name (no managed-by).
	squat := &towonelv1alpha1.TowonelAgent{}
	squat.Namespace = tunnel.Namespace
	squat.Name = defaultAgentName(tunnel.Namespace, tunnel.Name)
	squat.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: tunnel.Name, Namespace: tunnel.Namespace}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(squat).Build()

	var reason string
	ta, mode, err := resolveTarget(context.Background(), c, func(r, _ string) { reason = r }, "", tunnel, "")
	if err != nil || mode != modeSkip || reason != ReasonDefaultAgentClash {
		t.Fatalf("mode=%v reason=%q err=%v; want skip/%s", mode, reason, err, ReasonDefaultAgentClash)
	}
	if ta != nil {
		t.Fatalf("squat must not return a write target; got %+v", ta)
	}
}

func TestResolveTargetAgentRefCrossTunnelConflictSkips(t *testing.T) {
	tunnel := types.NamespacedName{Namespace: "net", Name: "app"}
	// Operator-owned agent bound to a DIFFERENT tunnel than the source's.
	other := &towonelv1alpha1.TowonelAgent{}
	other.Namespace, other.Name = "net", "other-agent"
	other.Labels = map[string]string{LabelManagedBy: ManagedByValue}
	other.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "different", Namespace: "net"}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(other).Build()

	var reason string
	ta, mode, err := resolveTarget(context.Background(), c, func(r, _ string) { reason = r }, "", tunnel, "other-agent")
	if err != nil || mode != modeSkip || reason != ReasonAgentRefConflict {
		t.Fatalf("mode=%v reason=%q err=%v; want skip/%s", mode, reason, err, ReasonAgentRefConflict)
	}
	if ta != nil {
		t.Fatalf("cross-tunnel agent-ref must not return a write target; got %+v", ta)
	}
}

func TestResolveTargetAgentRefOperatorOwnedSameTunnelWrites(t *testing.T) {
	tunnel := types.NamespacedName{Namespace: "net", Name: "app"}
	// Operator-owned agent bound to the SAME tunnel as the source's.
	mine := &towonelv1alpha1.TowonelAgent{}
	mine.Namespace, mine.Name = "net", "my-agent"
	mine.Labels = map[string]string{LabelManagedBy: ManagedByValue}
	mine.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: tunnel.Name, Namespace: tunnel.Namespace}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(mine).Build()

	ta, mode, err := resolveTarget(context.Background(), c, func(string, string) {}, "", tunnel, "my-agent")
	if err != nil || mode != modeWrite {
		t.Fatalf("mode=%v err=%v; want write", mode, err)
	}
	if ta == nil || ta.Namespace != "net" || ta.Name != "my-agent" {
		t.Fatalf("unexpected write target %+v", ta)
	}
}
