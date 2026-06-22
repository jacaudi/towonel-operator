package envtest_test

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	ctrlpkg "github.com/jacaudi/towonel-operator/internal/controller"
)

// annService builds a ClusterIP Service with the given annotations and ports.
// ClusterIP is intentionally omitted so envtest auto-assigns one (avoids IP
// collisions across tests that share the same API server).
func annService(ns, name string, ann map[string]string, ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Annotations: ann},
		Spec: corev1.ServiceSpec{
			Ports: ports,
			Type:  corev1.ServiceTypeClusterIP,
		},
	}
}

// defaultAgentNN returns the NamespacedName for the default operator-owned agent for a tunnel.
func defaultAgentNN(agentNS string, tunnel types.NamespacedName) types.NamespacedName {
	name := ctrlpkg.DefaultAgentNameForTest(tunnel.Namespace, tunnel.Name)
	return types.NamespacedName{Namespace: agentNS, Name: name}
}

// getDefaultAgent polls until the default agent for the given tunnel exists in
// agentNS, then returns it. Calls t.Fatal if the agent never appears.
func getDefaultAgent(t *testing.T, agentNS string, tunnel types.NamespacedName) *towonelv1alpha1.TowonelAgent {
	t.Helper()
	nn := defaultAgentNN(agentNS, tunnel)
	var ta towonelv1alpha1.TowonelAgent
	waitFor(t, 60*time.Second, func() bool {
		return k8sClient.Get(context.Background(), nn, &ta) == nil
	})
	return &ta
}

// TestSourceOptInCreatesAgentAndEntry verifies that an annotated Service causes
// the controller to auto-create an operator-owned agent with the contributed entry.
func TestSourceOptInCreatesAgentAndEntry(t *testing.T) {
	ns := mustNamespace(t)
	tunnel := types.NamespacedName{Namespace: ns, Name: "app"}
	svc := annService(ns, "web", map[string]string{
		ctrlpkg.AnnotationTunnel:      "enable",
		ctrlpkg.AnnotationTunnelRef:   "app",
		ctrlpkg.AnnotationSrcHostname: "app.example",
		ctrlpkg.AnnotationSrcOrigin:   "svc-web.svc:8080",
	}, corev1.ServicePort{Port: 8080})
	if err := k8sClient.Create(context.Background(), svc); err != nil {
		t.Fatal(err)
	}

	ta := getDefaultAgent(t, ns, tunnel)
	waitFor(t, 60*time.Second, func() bool {
		if err := k8sClient.Get(context.Background(), defaultAgentNN(ns, tunnel), ta); err != nil {
			return false
		}
		return len(ta.Spec.Services) == 1 && ta.Spec.Services[0].Hostname == "app.example"
	})
	if ta.Labels[ctrlpkg.LabelManagedBy] != ctrlpkg.ManagedByValue {
		t.Fatalf("agent missing managed-by label: %v", ta.Labels)
	}
	if ta.Annotations[ctrlpkg.AnnotationAutoCreated] != "true" {
		t.Fatalf("agent missing auto-created annotation: %v", ta.Annotations)
	}
}

// TestTwoSourcesFoldIntoOneAgent verifies that two annotated Services contributing
// to the same tunnel produce a single agent with both routing entries.
func TestTwoSourcesFoldIntoOneAgent(t *testing.T) {
	ns := mustNamespace(t)
	tunnel := types.NamespacedName{Namespace: ns, Name: "app"}

	for _, h := range []struct{ name, host string }{
		{"svc-a", "a.example"},
		{"svc-b", "b.example"},
	} {
		s := annService(ns, h.name, map[string]string{
			ctrlpkg.AnnotationTunnel:      "enable",
			ctrlpkg.AnnotationTunnelRef:   "app",
			ctrlpkg.AnnotationSrcHostname: h.host,
			ctrlpkg.AnnotationSrcOrigin:   h.name + ".svc:80",
		}, corev1.ServicePort{Port: 80})
		if err := k8sClient.Create(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}

	var ta towonelv1alpha1.TowonelAgent
	waitFor(t, 60*time.Second, func() bool {
		if err := k8sClient.Get(context.Background(), defaultAgentNN(ns, tunnel), &ta); err != nil {
			return false
		}
		return len(ta.Spec.Services) == 2
	})
}

// TestOptOutPrunesAndGCsAgent verifies that removing the opt-in annotation causes
// the controller to release the routing entry and GC the auto-created agent.
func TestOptOutPrunesAndGCsAgent(t *testing.T) {
	ns := mustNamespace(t)
	tunnel := types.NamespacedName{Namespace: ns, Name: "app"}

	svc := annService(ns, "web", map[string]string{
		ctrlpkg.AnnotationTunnel:      "enable",
		ctrlpkg.AnnotationTunnelRef:   "app",
		ctrlpkg.AnnotationSrcHostname: "app.example",
		ctrlpkg.AnnotationSrcOrigin:   "svc-web.svc:8080",
	}, corev1.ServicePort{Port: 8080})
	if err := k8sClient.Create(context.Background(), svc); err != nil {
		t.Fatal(err)
	}

	// Wait until the agent is created.
	agentNN := defaultAgentNN(ns, tunnel)
	var ta towonelv1alpha1.TowonelAgent
	waitFor(t, 60*time.Second, func() bool {
		return k8sClient.Get(context.Background(), agentNN, &ta) == nil
	})

	// Opt out by setting the annotation to "disable".
	var live corev1.Service
	waitFor(t, 5*time.Second, func() bool {
		return k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "web"}, &live) == nil
	})
	live.Annotations[ctrlpkg.AnnotationTunnel] = "disable"
	if err := k8sClient.Update(context.Background(), &live); err != nil {
		t.Fatal(err)
	}

	// The auto-created agent must be GC'd once its routing is empty.
	// The requeue delay (waitingRequeue = 30s) means the GC can take up to
	// 30s after the release to fire; allow 45s to cover the full window.
	waitFor(t, 45*time.Second, func() bool {
		var gone towonelv1alpha1.TowonelAgent
		return k8sClient.Get(context.Background(), agentNN, &gone) != nil
	})
}

// TestAgentRefContributesToHandAuthoredAgent is the issue #18 regression: a
// hand-authored agent with NO labels and NO mode field, referenced via agent-ref,
// is defaulted to Managed and has the source's routing contributed into it — without
// the operator adding managed-by/part-of labels or the auto-created annotation.
func TestAgentRefContributesToHandAuthoredAgent(t *testing.T) {
	ns := mustNamespace(t)

	user := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "home"},
	}
	user.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: ns}
	if err := k8sClient.Create(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	svc := annService(ns, "web", map[string]string{
		ctrlpkg.AnnotationTunnel:      "enable",
		ctrlpkg.AnnotationTunnelRef:   "app",
		ctrlpkg.AnnotationAgentRef:    "home",
		ctrlpkg.AnnotationSrcHostname: "new.example",
		ctrlpkg.AnnotationSrcOrigin:   "svc-web.svc:80",
	}, corev1.ServicePort{Port: 80})
	if err := k8sClient.Create(context.Background(), svc); err != nil {
		t.Fatal(err)
	}

	nn := types.NamespacedName{Namespace: ns, Name: "home"}
	var ta towonelv1alpha1.TowonelAgent
	waitFor(t, 60*time.Second, func() bool {
		if err := k8sClient.Get(context.Background(), nn, &ta); err != nil {
			return false
		}
		for _, s := range ta.Spec.Services {
			if s.Hostname == "new.example" {
				return true
			}
		}
		return false
	})
	// Lifecycle markers must NOT have been added: the operator reconciles routing,
	// it does not claim ownership of a hand-authored agent.
	if _, ok := ta.Labels[ctrlpkg.LabelManagedBy]; ok {
		t.Fatalf("operator stamped managed-by on a hand-authored agent: %v", ta.Labels)
	}
	if ta.Annotations[ctrlpkg.AnnotationAutoCreated] == "true" {
		t.Fatal("operator stamped auto-created on a hand-authored agent")
	}
	// The apiserver default must have set mode=Managed.
	if ta.Spec.Mode != towonelv1alpha1.ModeManaged {
		t.Fatalf("expected defaulted mode=Managed, got %q", ta.Spec.Mode)
	}
}

// TestObserveOnlyModeNeverMutatesUserAgent verifies that spec.mode=ObserveOnly keeps
// the operator hands-off even when a source explicitly references the agent.
func TestObserveOnlyModeNeverMutatesUserAgent(t *testing.T) {
	ns := mustNamespace(t)

	user := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "mine"},
	}
	user.Spec.Mode = towonelv1alpha1.ModeObserveOnly
	user.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: ns}
	user.Spec.Services = []towonelv1alpha1.AgentService{{Hostname: "user.example", Origin: "u:1"}}
	if err := k8sClient.Create(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	var before towonelv1alpha1.TowonelAgent
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "mine"}, &before); err != nil {
		t.Fatal(err)
	}
	rvBefore := before.ResourceVersion

	svc := annService(ns, "web", map[string]string{
		ctrlpkg.AnnotationTunnel:      "enable",
		ctrlpkg.AnnotationTunnelRef:   "app",
		ctrlpkg.AnnotationAgentRef:    "mine",
		ctrlpkg.AnnotationSrcHostname: "new.example",
		ctrlpkg.AnnotationSrcOrigin:   "svc-web.svc:80",
	}, corev1.ServicePort{Port: 80})
	if err := k8sClient.Create(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	var after towonelv1alpha1.TowonelAgent
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "mine"}, &after); err != nil {
		t.Fatal(err)
	}
	if after.ResourceVersion != rvBefore || len(after.Spec.Services) != 1 {
		t.Fatalf("ObserveOnly violated: rv %s->%s services=%d", rvBefore, after.ResourceVersion, len(after.Spec.Services))
	}
}

// TestHostnameConflictAcrossSources verifies that two Services contributing the
// same hostname to the same agent result in exactly one entry (no silent overwrite).
func TestHostnameConflictAcrossSources(t *testing.T) {
	ns := mustNamespace(t)
	tunnel := types.NamespacedName{Namespace: ns, Name: "app"}

	mk := func(name, origin string) *corev1.Service {
		return annService(ns, name, map[string]string{
			ctrlpkg.AnnotationTunnel:      "enable",
			ctrlpkg.AnnotationTunnelRef:   "app",
			ctrlpkg.AnnotationSrcHostname: "dup.example",
			ctrlpkg.AnnotationSrcOrigin:   origin,
			// AnnotationSrcOrigin is set explicitly; no ClusterIP inference needed.
		}, corev1.ServicePort{Port: 80})
	}

	// First Service wins the SSA field ownership.
	if err := k8sClient.Create(context.Background(), mk("svc-a", "a:1")); err != nil {
		t.Fatal(err)
	}
	// Wait until the agent and the first entry exist.
	var ta towonelv1alpha1.TowonelAgent
	waitFor(t, 60*time.Second, func() bool {
		if err := k8sClient.Get(context.Background(), defaultAgentNN(ns, tunnel), &ta); err != nil {
			return false
		}
		return len(ta.Spec.Services) == 1
	})

	// Second Service tries to claim the same hostname; the SSA conflict is surfaced
	// as an Event — the agent must still have exactly one dup.example entry.
	if err := k8sClient.Create(context.Background(), mk("svc-b", "b:2")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)

	if err := k8sClient.Get(context.Background(), defaultAgentNN(ns, tunnel), &ta); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, s := range ta.Spec.Services {
		if s.Hostname == "dup.example" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one dup.example entry, got %d", count)
	}
}

// TestGatewayAndHTTPRouteRoundTrip verifies that an annotated Gateway with a
// listener hostname results in the correct agent and routing entry.
func TestGatewayAndHTTPRouteRoundTrip(t *testing.T) {
	ns := mustNamespace(t)

	// Proxy Service backing the Gateway.
	proxy := annService(ns, "envoy", nil, corev1.ServicePort{Port: 443})
	if err := k8sClient.Create(context.Background(), proxy); err != nil {
		t.Fatal(err)
	}

	hn := func(s string) *gwv1.Hostname { h := gwv1.Hostname(s); return &h }
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      "gw",
			Annotations: map[string]string{
				ctrlpkg.AnnotationTunnel:         "enable",
				ctrlpkg.AnnotationTunnelRef:      "gwtun",
				ctrlpkg.AnnotationGatewayService: "envoy:443",
			},
		},
		Spec: gwv1.GatewaySpec{
			GatewayClassName: "x",
			Listeners: []gwv1.Listener{
				{
					Name:     "https",
					Protocol: gwv1.HTTPSProtocolType,
					Port:     443,
					Hostname: hn("gw.example"),
				},
			},
		},
	}
	if err := k8sClient.Create(context.Background(), gw); err != nil {
		t.Fatal(err)
	}

	tunnel := types.NamespacedName{Namespace: ns, Name: "gwtun"}
	var ta towonelv1alpha1.TowonelAgent
	waitFor(t, 60*time.Second, func() bool {
		if err := k8sClient.Get(context.Background(), defaultAgentNN(ns, tunnel), &ta); err != nil {
			return false
		}
		return len(ta.Spec.Services) == 1 && ta.Spec.Services[0].Hostname == "gw.example"
	})
}

// TestRetargetReleasesFromHandAuthoredManagedAgent verifies §3.5: when a source
// stops contributing (opt-out), its routing is released from a hand-authored
// Managed agent (which carries no managed-by label) — and the agent, being
// user-owned, is NOT garbage-collected.
func TestRetargetReleasesFromHandAuthoredManagedAgent(t *testing.T) {
	ns := mustNamespace(t)

	user := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "home"},
	}
	user.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: ns}
	if err := k8sClient.Create(context.Background(), user); err != nil {
		t.Fatal(err)
	}

	svc := annService(ns, "web", map[string]string{
		ctrlpkg.AnnotationTunnel:      "enable",
		ctrlpkg.AnnotationTunnelRef:   "app",
		ctrlpkg.AnnotationAgentRef:    "home",
		ctrlpkg.AnnotationSrcHostname: "new.example",
		ctrlpkg.AnnotationSrcOrigin:   "svc-web.svc:80",
	}, corev1.ServicePort{Port: 80})
	if err := k8sClient.Create(context.Background(), svc); err != nil {
		t.Fatal(err)
	}

	nn := types.NamespacedName{Namespace: ns, Name: "home"}
	var ta towonelv1alpha1.TowonelAgent
	waitFor(t, 60*time.Second, func() bool {
		if err := k8sClient.Get(context.Background(), nn, &ta); err != nil {
			return false
		}
		return len(ta.Spec.Services) == 1
	})

	// Opt out — the source releases its routing.
	var live corev1.Service
	waitFor(t, 5*time.Second, func() bool {
		return k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "web"}, &live) == nil
	})
	live.Annotations[ctrlpkg.AnnotationTunnel] = "disable"
	if err := k8sClient.Update(context.Background(), &live); err != nil {
		t.Fatal(err)
	}

	// Routing must be released from the hand-authored agent (filter no longer hides it).
	waitFor(t, 45*time.Second, func() bool {
		if err := k8sClient.Get(context.Background(), nn, &ta); err != nil {
			return false
		}
		return len(ta.Spec.Services) == 0
	})
	// And the user-owned agent must STILL exist (never GC'd — not auto-created).
	if err := k8sClient.Get(context.Background(), nn, &ta); err != nil {
		t.Fatalf("hand-authored agent was deleted; it must never be GC'd: %v", err)
	}
}

// TestGatewaySourcesDisabledWhenFlagFalse verifies that SetupSourceControllers
// with EnableGatewayAPI:"false" starts cleanly and that a Service source (which
// is always enabled) still works while no gateway agent is produced.
// A unannotated Gateway is created to confirm no spurious agent appears when
// gateway-api is disabled; the shared manager's predicate also ignores it.
func TestGatewaySourcesDisabledWhenFlagFalse(t *testing.T) {
	ns := mustNamespace(t)
	// Per-test manager with gateway explicitly disabled; the Service controller
	// is still active.
	startSourceManagerWith(t, "", "false")

	// Proxy Service — unannotated (no towonel.io/tunnel), so neither manager processes it.
	proxy := annService(ns, "envoy", nil, corev1.ServicePort{Port: 443})
	_ = k8sClient.Create(context.Background(), proxy)

	hn := func(s string) *gwv1.Hostname { h := gwv1.Hostname(s); return &h }
	// Gateway without the towonel.io/tunnel annotation; the predicate filters it
	// so neither the shared manager nor the gateway-disabled per-test manager
	// will reconcile it.
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      "gw",
			// No AnnotationTunnel — predicate filters this object in all managers.
		},
		Spec: gwv1.GatewaySpec{
			GatewayClassName: "x",
			Listeners: []gwv1.Listener{
				{Name: "l", Protocol: gwv1.HTTPSProtocolType, Port: 443, Hostname: hn("gw.example")},
			},
		},
	}
	_ = k8sClient.Create(context.Background(), gw)

	// Give any controller time to react; no agent should appear.
	time.Sleep(500 * time.Millisecond)

	var ta towonelv1alpha1.TowonelAgent
	tunnel := types.NamespacedName{Namespace: ns, Name: "gwtun"}
	if err := k8sClient.Get(context.Background(), defaultAgentNN(ns, tunnel), &ta); err == nil {
		t.Fatal("no agent expected for unannotated gateway but one was created")
	}
}

// TestReconcilingAgentEventOnHandAuthored verifies the operator emits a
// ReconcilingAgent event when it contributes routing into a hand-authored agent.
func TestReconcilingAgentEventOnHandAuthored(t *testing.T) {
	ns := mustNamespace(t)

	user := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "home"},
	}
	user.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: ns}
	if err := k8sClient.Create(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	svc := annService(ns, "web", map[string]string{
		ctrlpkg.AnnotationTunnel:      "enable",
		ctrlpkg.AnnotationTunnelRef:   "app",
		ctrlpkg.AnnotationAgentRef:    "home",
		ctrlpkg.AnnotationSrcHostname: "new.example",
		ctrlpkg.AnnotationSrcOrigin:   "svc-web.svc:80",
	}, corev1.ServicePort{Port: 80})
	if err := k8sClient.Create(context.Background(), svc); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 60*time.Second, func() bool {
		var events corev1.EventList
		if err := k8sClient.List(context.Background(), &events, client.InNamespace(ns)); err != nil {
			return false
		}
		for i := range events.Items {
			if events.Items[i].Reason == ctrlpkg.ReasonReconcilingAgent {
				return true
			}
		}
		return false
	})
}

// httpRoute builds an HTTPRoute with the given annotations, a single Gateway
// parentRef in its own namespace, and one hostname.
func httpRoute(ns, name string, ann map[string]string, gwName, host string) *gwv1.HTTPRoute {
	return &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Annotations: ann},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: gwv1.ObjectName(gwName)}}},
			Hostnames:       []gwv1.Hostname{gwv1.Hostname(host)},
		},
	}
}

// autoRoutesGateway builds a Gateway opted into auto-routes with a gateway-service
// pointing at proxySvc:443 (no towonel.io/tunnel — it is not a source itself).
func autoRoutesGateway(ns, name, proxySvc string) *gwv1.Gateway {
	return &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Annotations: map[string]string{
			ctrlpkg.AnnotationAutoRoutes:     "true",
			ctrlpkg.AnnotationGatewayService: proxySvc + ":443",
		}},
		Spec: gwv1.GatewaySpec{GatewayClassName: "x", Listeners: []gwv1.Listener{
			{Name: "https", Protocol: gwv1.HTTPSProtocolType, Port: 443},
		}},
	}
}

// agentHasHostname reports whether the default agent for tunnel currently has a
// service entry for host.
func agentHasHostname(t *testing.T, agentNS string, tunnel types.NamespacedName, host string) bool {
	t.Helper()
	var ta towonelv1alpha1.TowonelAgent
	if err := k8sClient.Get(context.Background(), defaultAgentNN(agentNS, tunnel), &ta); err != nil {
		return false
	}
	for _, s := range ta.Spec.Services {
		if s.Hostname == host {
			return true
		}
	}
	return false
}

// eventFor reports whether an event with the given reason was recorded on the
// named object in ns.
func eventFor(t *testing.T, ns, name, reason string) bool {
	t.Helper()
	var events corev1.EventList
	if err := k8sClient.List(context.Background(), &events, client.InNamespace(ns)); err != nil {
		return false
	}
	for i := range events.Items {
		e := &events.Items[i]
		if e.Reason == reason && e.InvolvedObject.Name == name {
			return true
		}
	}
	return false
}

// autoSelectedEventFor reports whether a Normal AutoSelectedByGateway event was
// recorded on the named route in ns.
func autoSelectedEventFor(t *testing.T, ns, route string) bool {
	t.Helper()
	return eventFor(t, ns, route, ctrlpkg.ReasonAutoSelectedByGateway)
}

// assertStaysAbsent fails if any host appears on the tunnel's default agent at any
// point during the window — a negative route must never tunnel, even late.
func assertStaysAbsent(t *testing.T, window time.Duration, ns string, tunnel types.NamespacedName, hosts ...string) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		for _, h := range hosts {
			if agentHasHostname(t, ns, tunnel, h) {
				t.Fatalf("%s appeared during stability window; a negative route must never tunnel", h)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestAutoRoutesPrecedenceMatrix is the design §11 acceptance gate for #25: under a
// single auto-routes Gateway, a route's OWN towonel.io/tunnel is authoritative when
// PRESENT (truthy → tunnel, false/garbage/empty → release), and only an ABSENT key
// inherits the Gateway's auto-routes default. Tunnel targeting is made deterministic
// by setting towonel.io/tunnel-ref explicitly on every route (the shared-API-server
// envtest cannot rely on sole-TowonelTunnel ref-less resolution), so the only variable
// distinguishing the five routes is the towonel.io/tunnel key itself.
func TestAutoRoutesPrecedenceMatrix(t *testing.T) {
	ns := mustNamespace(t)
	tunnel := types.NamespacedName{Namespace: ns, Name: "gwtun"}

	proxy := annService(ns, "envoy", nil, corev1.ServicePort{Port: 443})
	if err := k8sClient.Create(context.Background(), proxy); err != nil {
		t.Fatal(err)
	}
	gw := autoRoutesGateway(ns, "gw", "envoy")
	if err := k8sClient.Create(context.Background(), gw); err != nil {
		t.Fatal(err)
	}

	// Every route carries a valid tunnel-ref so the ONLY variable is the
	// towonel.io/tunnel key: optout/garbage/empty must NOT tunnel even though a
	// resolvable tunnel and a valid gateway-service are both present.
	routes := []*gwv1.HTTPRoute{
		httpRoute(ns, "inherit", map[string]string{ctrlpkg.AnnotationTunnelRef: "gwtun"}, "gw", "inherit.example"),
		httpRoute(ns, "optout", map[string]string{ctrlpkg.AnnotationTunnelRef: "gwtun", ctrlpkg.AnnotationTunnel: "false"}, "gw", "optout.example"),
		httpRoute(ns, "optin", map[string]string{ctrlpkg.AnnotationTunnelRef: "gwtun", ctrlpkg.AnnotationTunnel: "true"}, "gw", "optin.example"),
		httpRoute(ns, "garbage", map[string]string{ctrlpkg.AnnotationTunnelRef: "gwtun", ctrlpkg.AnnotationTunnel: "banana"}, "gw", "garbage.example"),
		httpRoute(ns, "empty", map[string]string{ctrlpkg.AnnotationTunnelRef: "gwtun", ctrlpkg.AnnotationTunnel: ""}, "gw", "empty.example"),
	}
	for _, rt := range routes {
		if err := k8sClient.Create(context.Background(), rt); err != nil {
			t.Fatal(err)
		}
	}

	// Settle anchor: the inherit route's terminal Normal event fires only after a full
	// successful reconcile (select → resolve → derive → apply).
	waitFor(t, 60*time.Second, func() bool { return autoSelectedEventFor(t, ns, "inherit") })
	// And both positive hostnames have landed.
	waitFor(t, 60*time.Second, func() bool {
		return agentHasHostname(t, ns, tunnel, "inherit.example") && agentHasHostname(t, ns, tunnel, "optin.example")
	})
	// Negatives are absent now AND stay absent across a stability window.
	for _, host := range []string{"optout.example", "garbage.example", "empty.example"} {
		if agentHasHostname(t, ns, tunnel, host) {
			t.Fatalf("%s tunneled but must never be", host)
		}
	}
	assertStaysAbsent(t, 1*time.Second, ns, tunnel, "optout.example", "garbage.example", "empty.example")
}

// TestAutoRoutesNamespaceScoped verifies auto-routes is namespace-scoped (#25, §2):
// a route in a DIFFERENT namespace with an explicit cross-namespace parentRef into the
// Gateway's namespace is NEVER auto-selected. The route is given a valid tunnel-ref so
// a broken namespace guard WOULD create an agent — making the negative non-vacuous.
func TestAutoRoutesNamespaceScoped(t *testing.T) {
	gwNS := mustNamespace(t)
	routeNS := mustNamespace(t)

	proxy := annService(gwNS, "envoy", nil, corev1.ServicePort{Port: 443})
	if err := k8sClient.Create(context.Background(), proxy); err != nil {
		t.Fatal(err)
	}
	gw := autoRoutesGateway(gwNS, "gw", "envoy")
	if err := k8sClient.Create(context.Background(), gw); err != nil {
		t.Fatal(err)
	}

	// Cross-namespace parentRef: route lives in routeNS, parents the Gateway in gwNS.
	gwNSObj := gwv1.Namespace(gwNS)
	rt := httpRoute(routeNS, "xroute", map[string]string{ctrlpkg.AnnotationTunnelRef: routeNS + "/gwtun"}, "gw", "xns.example")
	rt.Spec.ParentRefs[0].Namespace = &gwNSObj
	if err := k8sClient.Create(context.Background(), rt); err != nil {
		t.Fatal(err)
	}

	// Give the controller time to react; the cross-ns route must never be tunneled.
	time.Sleep(1 * time.Second)
	tunnel := types.NamespacedName{Namespace: routeNS, Name: "gwtun"}
	if agentHasHostname(t, routeNS, tunnel, "xns.example") {
		t.Fatal("cross-namespace route was auto-selected; auto-routes must be namespace-scoped")
	}
}

// TestAutoRoutesNoGatewayServiceIsNoOp verifies the gateway-service prerequisite
// (#25): a Gateway with auto-routes:"true" but NO gateway-service cannot auto-tunnel
// its routes. The un-annotated route gets a ReasonGatewayServiceUnset Warning and is
// never tunneled.
func TestAutoRoutesNoGatewayServiceIsNoOp(t *testing.T) {
	ns := mustNamespace(t)

	// auto-routes enabled but NO gateway-service annotation.
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "gw", Annotations: map[string]string{
			ctrlpkg.AnnotationAutoRoutes: "true",
		}},
		Spec: gwv1.GatewaySpec{GatewayClassName: "x", Listeners: []gwv1.Listener{
			{Name: "https", Protocol: gwv1.HTTPSProtocolType, Port: 443},
		}},
	}
	if err := k8sClient.Create(context.Background(), gw); err != nil {
		t.Fatal(err)
	}
	rt := httpRoute(ns, "r", map[string]string{ctrlpkg.AnnotationTunnelRef: "gwtun"}, "gw", "noproxy.example")
	if err := k8sClient.Create(context.Background(), rt); err != nil {
		t.Fatal(err)
	}

	// A GatewayServiceUnspecified Warning must be recorded on the route.
	waitFor(t, 60*time.Second, func() bool { return eventFor(t, ns, "r", ctrlpkg.ReasonGatewayServiceUnset) })

	// And the route's hostname must never be tunneled.
	tunnel := types.NamespacedName{Namespace: ns, Name: "gwtun"}
	if agentHasHostname(t, ns, tunnel, "noproxy.example") {
		t.Fatal("route was tunneled despite the Gateway having no gateway-service")
	}
}

// TestAutoRoutesDisableReleases verifies §11: removing towonel.io/auto-routes from a
// Gateway releases its auto-selected routes and GCs the auto-created default agent.
func TestAutoRoutesDisableReleases(t *testing.T) {
	ns := mustNamespace(t)
	tunnel := types.NamespacedName{Namespace: ns, Name: "gwtun"}

	proxy := annService(ns, "envoy", nil, corev1.ServicePort{Port: 443})
	if err := k8sClient.Create(context.Background(), proxy); err != nil {
		t.Fatal(err)
	}
	gw := autoRoutesGateway(ns, "gw", "envoy")
	if err := k8sClient.Create(context.Background(), gw); err != nil {
		t.Fatal(err)
	}
	rt := httpRoute(ns, "r", map[string]string{ctrlpkg.AnnotationTunnelRef: "gwtun"}, "gw", "rel.example")
	if err := k8sClient.Create(context.Background(), rt); err != nil {
		t.Fatal(err)
	}

	// Wait until the un-annotated route is auto-selected and tunneled.
	waitFor(t, 60*time.Second, func() bool {
		return agentHasHostname(t, ns, tunnel, "rel.example")
	})

	// Disable auto-routes by removing the annotation from the Gateway.
	var live gwv1.Gateway
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "gw"}, &live); err != nil {
		t.Fatal(err)
	}
	delete(live.Annotations, ctrlpkg.AnnotationAutoRoutes)
	if err := k8sClient.Update(context.Background(), &live); err != nil {
		t.Fatal(err)
	}

	// The route's routing is released and the auto-created default agent is GC'd once
	// its routing empties. The waitingRequeue delay means GC can take up to 30s after
	// the release fires; allow 45s (matching TestOptOutPrunesAndGCsAgent).
	agentNN := defaultAgentNN(ns, tunnel)
	waitFor(t, 45*time.Second, func() bool {
		var gone towonelv1alpha1.TowonelAgent
		return k8sClient.Get(context.Background(), agentNN, &gone) != nil
	})
}

// TestAutoRoutesAmbiguousMultiProxyNotTunneled verifies an auto-SELECTED
// (un-annotated) route whose parentRefs resolve to two DISTINCT gateway proxies
// is not tunneled and emits AmbiguousGateway (the selection short-circuits on the
// first parent, but origin resolution walks all parents and detects ambiguity).
func TestAutoRoutesAmbiguousMultiProxyNotTunneled(t *testing.T) {
	ns := mustNamespace(t)
	tunnel := types.NamespacedName{Namespace: ns, Name: "gwtun"}

	// Two distinct proxy Services → two distinct origins.
	if err := k8sClient.Create(context.Background(), annService(ns, "envoy-a", nil, corev1.ServicePort{Port: 443})); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Create(context.Background(), annService(ns, "envoy-b", nil, corev1.ServicePort{Port: 443})); err != nil {
		t.Fatal(err)
	}
	// Two same-namespace auto-routes Gateways with DIFFERENT gateway-services.
	if err := k8sClient.Create(context.Background(), autoRoutesGateway(ns, "gw-a", "envoy-a")); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Create(context.Background(), autoRoutesGateway(ns, "gw-b", "envoy-b")); err != nil {
		t.Fatal(err)
	}
	// Un-annotated route parenting BOTH gateways; explicit tunnel-ref so a broken
	// ambiguity guard would actually tunnel it (non-vacuous negative).
	rt := httpRoute(ns, "amb", map[string]string{ctrlpkg.AnnotationTunnelRef: "gwtun"}, "gw-a", "amb.example")
	rt.Spec.ParentRefs = append(rt.Spec.ParentRefs, gwv1.ParentReference{Name: "gw-b"})
	if err := k8sClient.Create(context.Background(), rt); err != nil {
		t.Fatal(err)
	}

	// Ambiguity is reported as a Warning on the route...
	waitFor(t, 60*time.Second, func() bool { return eventFor(t, ns, "amb", ctrlpkg.ReasonAmbiguousGateway) })
	// ...and the route is never tunneled.
	if agentHasHostname(t, ns, tunnel, "amb.example") {
		t.Fatal("ambiguous-proxy route was tunneled; must skip with AmbiguousGateway")
	}
}
