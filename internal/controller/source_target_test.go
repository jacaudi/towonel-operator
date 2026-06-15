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

func TestResolveTunnel(t *testing.T) {
	newTunnel := func(ns, name string) *towonelv1alpha1.TowonelTunnel {
		tn := &towonelv1alpha1.TowonelTunnel{}
		tn.Namespace, tn.Name = ns, name
		return tn
	}
	cases := []struct {
		name             string
		raw, srcNS       string
		tunnels          []*towonelv1alpha1.TowonelTunnel
		wantNS, wantName string
		wantOK           bool
	}{
		{name: "explicit bare", raw: "main", srcNS: "src", wantNS: "src", wantName: "main", wantOK: true},
		{name: "explicit qualified", raw: "net/main", srcNS: "src", wantNS: "net", wantName: "main", wantOK: true},
		{name: "empty defaults to sole", srcNS: "src", tunnels: []*towonelv1alpha1.TowonelTunnel{newTunnel("net", "main")}, wantNS: "net", wantName: "main", wantOK: true},
		{name: "empty with none skips", srcNS: "src", wantOK: false},
		{name: "empty with many skips", srcNS: "src", tunnels: []*towonelv1alpha1.TowonelTunnel{newTunnel("net", "a"), newTunnel("net", "b")}, wantOK: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := fake.NewClientBuilder().WithScheme(srcScheme(t))
			for _, tn := range c.tunnels {
				b = b.WithObjects(tn)
			}
			var reason string
			got, ok, err := resolveTunnel(context.Background(), b.Build(), func(r, _ string) { reason = r }, c.raw, c.srcNS)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if ok != c.wantOK {
				t.Fatalf("ok=%v want %v (reason %q)", ok, c.wantOK, reason)
			}
			if !c.wantOK {
				if reason != ReasonTunnelRefMissing {
					t.Fatalf("skip reason=%q; want %s", reason, ReasonTunnelRefMissing)
				}
				return
			}
			if got.Namespace != c.wantNS || got.Name != c.wantName {
				t.Fatalf("got %v; want %s/%s", got, c.wantNS, c.wantName)
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

func TestResolveTargetDefaultAdoptsSoleAgent(t *testing.T) {
	// No agent-ref + exactly one existing agent (not at the default name) -> adopt
	// it instead of minting an operator-owned default (single-agent deployment).
	mine := &towonelv1alpha1.TowonelAgent{}
	mine.Namespace, mine.Name = "net", "home"
	mine.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: "net"}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(mine).Build()

	tunnel := types.NamespacedName{Namespace: "net", Name: "app"}
	ta, mode, err := resolveTarget(context.Background(), c, func(string, string) {}, "", tunnel, "")
	if err != nil || mode != modeWrite {
		t.Fatalf("mode=%v err=%v; want write into the sole agent", mode, err)
	}
	if ta == nil || ta.Name != "home" {
		t.Fatalf("unexpected target %+v; want adopted sole agent", ta)
	}
	// And it must NOT have auto-created a second (default-named) agent.
	var list towonelv1alpha1.TowonelAgentList
	_ = c.List(context.Background(), &list)
	if len(list.Items) != 1 {
		t.Fatalf("sole-agent default must not mint another agent; found %d", len(list.Items))
	}
}

func TestResolveTargetDefaultSoleAgentCrossTunnelSkips(t *testing.T) {
	// The sole agent is validated like an explicit ref: bound elsewhere -> skip.
	other := &towonelv1alpha1.TowonelAgent{}
	other.Namespace, other.Name = "net", "home"
	other.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "different", Namespace: "net"}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(other).Build()

	var reason string
	ta, mode, err := resolveTarget(context.Background(), c, func(r, _ string) { reason = r }, "", types.NamespacedName{Namespace: "net", Name: "app"}, "")
	if err != nil || mode != modeSkip || reason != ReasonAgentRefConflict {
		t.Fatalf("mode=%v reason=%q err=%v; want skip/%s", mode, reason, err, ReasonAgentRefConflict)
	}
	if ta != nil {
		t.Fatalf("cross-tunnel sole agent must not return a write target; got %+v", ta)
	}
}

func TestResolveTargetDefaultMultipleAgentsMintsDefault(t *testing.T) {
	// Two agents -> ambiguous, so fall back to the operator-owned default.
	a1 := &towonelv1alpha1.TowonelAgent{}
	a1.Namespace, a1.Name = "net", "a1"
	a1.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: "net"}
	a2 := &towonelv1alpha1.TowonelAgent{}
	a2.Namespace, a2.Name = "net", "a2"
	a2.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: "net"}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(a1, a2).Build()

	tunnel := types.NamespacedName{Namespace: "net", Name: "app"}
	ta, mode, err := resolveTarget(context.Background(), c, func(string, string) {}, "", tunnel, "")
	if err != nil || mode != modeWrite {
		t.Fatalf("mode=%v err=%v; want write into minted default", mode, err)
	}
	if !agentIsOperatorOwned(ta) || ta.Name != defaultAgentName("net", "app") {
		t.Fatalf("ambiguous agents must mint the operator-owned default; got %+v", ta)
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
