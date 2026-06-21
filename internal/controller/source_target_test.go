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

func TestResolveTunnelOmissionDefault(t *testing.T) {
	mkTunnel := func(ns, name string) *towonelv1alpha1.TowonelTunnel {
		tt := &towonelv1alpha1.TowonelTunnel{}
		tt.Namespace, tt.Name = ns, name
		return tt
	}
	cases := []struct {
		name       string
		raw, srcNS string
		tunnels    []*towonelv1alpha1.TowonelTunnel
		wantNN     types.NamespacedName
		wantOK     bool
		wantReason string // "" => no Event expected
	}{
		{
			name:    "empty ref + exactly one tunnel resolves to it",
			raw:     "",
			srcNS:   "src",
			tunnels: []*towonelv1alpha1.TowonelTunnel{mkTunnel("net", "only")},
			wantNN:  types.NamespacedName{Namespace: "net", Name: "only"},
			wantOK:  true,
		},
		{
			name:       "empty ref + zero tunnels skips loudly",
			raw:        "",
			srcNS:      "src",
			tunnels:    nil,
			wantOK:     false,
			wantReason: ReasonTunnelRefMissing,
		},
		{
			name:       "empty ref + multiple tunnels skips loudly",
			raw:        "",
			srcNS:      "src",
			tunnels:    []*towonelv1alpha1.TowonelTunnel{mkTunnel("net", "a"), mkTunnel("net", "b")},
			wantOK:     false,
			wantReason: ReasonTunnelRefMissing,
		},
		{
			name:    "non-empty bare ref parses unchanged (ignores tunnel count)",
			raw:     "app",
			srcNS:   "src",
			tunnels: []*towonelv1alpha1.TowonelTunnel{mkTunnel("net", "a"), mkTunnel("net", "b")},
			wantNN:  types.NamespacedName{Namespace: "src", Name: "app"},
			wantOK:  true,
		},
		{
			name:    "non-empty qualified ref parses unchanged",
			raw:     "net/app",
			srcNS:   "src",
			tunnels: nil,
			wantNN:  types.NamespacedName{Namespace: "net", Name: "app"},
			wantOK:  true,
		},
		{
			name:       "non-empty malformed ref skips loudly",
			raw:        "net/",
			srcNS:      "src",
			tunnels:    []*towonelv1alpha1.TowonelTunnel{mkTunnel("net", "only")},
			wantOK:     false,
			wantReason: ReasonTunnelRefMissing,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := fake.NewClientBuilder().WithScheme(srcScheme(t))
			for _, tt := range c.tunnels {
				b = b.WithObjects(tt)
			}
			cl := b.Build()
			var reason string
			nn, ok, err := resolveTunnel(context.Background(), cl, func(r, _ string) { reason = r }, c.raw, c.srcNS)
			if err != nil {
				t.Fatalf("unexpected transient error: %v", err)
			}
			if ok != c.wantOK {
				t.Fatalf("ok=%v want %v", ok, c.wantOK)
			}
			if c.wantOK && nn != c.wantNN {
				t.Fatalf("nn=%v want %v", nn, c.wantNN)
			}
			if reason != c.wantReason {
				t.Fatalf("reason=%q want %q", reason, c.wantReason)
			}
		})
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

func TestResolveTargetAgentRefUnlabeledDefaultsToWrite(t *testing.T) {
	// #18 regression: a hand-authored agent with NO labels and NO mode field,
	// referenced via agent-ref with a matching tunnelRef, defaults to Managed -> write.
	mine := &towonelv1alpha1.TowonelAgent{}
	mine.Namespace, mine.Name = "net", "home"
	mine.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: "net"}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(mine).Build()
	ta, mode, err := resolveTarget(context.Background(), c, func(string, string) {}, "", types.NamespacedName{Namespace: "net", Name: "app"}, "home")
	if err != nil || mode != modeWrite {
		t.Fatalf("mode=%v err=%v; want write (unlabeled no-mode agent defaults to Managed)", mode, err)
	}
	if ta == nil || ta.Name != "home" {
		t.Fatalf("unexpected target %+v", ta)
	}
}

func TestResolveTargetAgentRefObserveOnlyMode(t *testing.T) {
	// Explicit ObserveOnly is honored regardless of ownership label.
	mine := &towonelv1alpha1.TowonelAgent{}
	mine.Namespace, mine.Name = "net", "mine"
	mine.Spec.Mode = towonelv1alpha1.ModeObserveOnly
	mine.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: "net"}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(mine).Build()
	_, mode, err := resolveTarget(context.Background(), c, func(string, string) {}, "", types.NamespacedName{Namespace: "net", Name: "app"}, "mine")
	if err != nil || mode != modeObserve {
		t.Fatalf("mode=%v err=%v; want observe (explicit ObserveOnly)", mode, err)
	}
}

func TestAgentModeDefaultsToManaged(t *testing.T) {
	var ta towonelv1alpha1.TowonelAgent
	if got := agentMode(&ta); got != towonelv1alpha1.ModeManaged {
		t.Fatalf("empty mode = %q; want Managed", got)
	}
	ta.Spec.Mode = towonelv1alpha1.ModeObserveOnly
	if got := agentMode(&ta); got != towonelv1alpha1.ModeObserveOnly {
		t.Fatalf("explicit mode = %q; want ObserveOnly", got)
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
