package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// nsPtr returns a pointer to a gwv1.Namespace for use in parentRef fixtures.
func nsPtr(s string) *gwv1.Namespace { n := gwv1.Namespace(s); return &n }

func TestRoutesForGatewayMatchesByDefaultedNamespace(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "kgateway"}}
	// route A: parentRef nil-namespace, same namespace as gateway → MUST match (defaults to route ns == kgateway)
	routeA := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "kgateway", Annotations: map[string]string{AnnotationTunnel: "enable"}},
		Spec:       gwv1.HTTPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "external"}}}},
	}
	// route B: explicit cross-namespace parentRef → MUST match
	routeB := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "immich", Annotations: map[string]string{AnnotationTunnel: "enable"}},
		Spec:       gwv1.HTTPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "external", Namespace: nsPtr("kgateway")}}}},
	}
	// route C: un-annotated → MUST NOT match
	routeC := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "immich"},
		Spec:       gwv1.HTTPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "external", Namespace: nsPtr("kgateway")}}}},
	}
	// route D: annotated, but parentRef targets a DIFFERENT gateway name → MUST NOT match (locks name/namespace scoping)
	routeD := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "d", Namespace: "immich", Annotations: map[string]string{AnnotationTunnel: "enable"}},
		Spec:       gwv1.HTTPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "other", Namespace: nsPtr("kgateway")}}}},
	}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(routeA, routeB, routeC, routeD).Build()
	reqs := (&HTTPRouteSourceReconciler{Client: c}).routesForGateway(context.Background(), gw)
	got := map[string]bool{}
	for _, r := range reqs {
		got[r.NamespacedName.String()] = true
	}
	if len(reqs) != 2 || !got["kgateway/a"] || !got["immich/b"] {
		t.Fatalf("want exactly {kgateway/a, immich/b}, got %v", reqs)
	}
}

func TestSourcesForAgentHTTPRouteMatchesByAgentRefAndNamespace(t *testing.T) {
	agent := &towonelv1alpha1.TowonelAgent{}
	agent.Namespace, agent.Name = "app", "my-agent"
	// route A: agent-ref==my-agent, bare tunnel-ref → resolves to route ns "app" == agent ns → MUST match
	routeA := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "app",
		Annotations: map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "t", AnnotationAgentRef: "my-agent"}}}
	// route B: agent-ref names a DIFFERENT agent → MUST NOT match
	routeB := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "app",
		Annotations: map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "t", AnnotationAgentRef: "other-agent"}}}
	// route C: no agent-ref (default-agent path) → MUST NOT match (default path can't strand; see sourceTargetsAgent)
	routeC := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "app",
		Annotations: map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "t"}}}
	// route D: agent-ref==my-agent but tunnel-ref resolves to ns "other" != agent ns "app" → MUST NOT match
	routeD := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "d", Namespace: "app",
		Annotations: map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "other/t", AnnotationAgentRef: "my-agent"}}}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(routeA, routeB, routeC, routeD).Build()
	reqs := (&HTTPRouteSourceReconciler{Client: c}).sourcesForAgent(context.Background(), agent)
	if len(reqs) != 1 || reqs[0].NamespacedName.String() != "app/a" {
		t.Fatalf("want exactly {app/a}, got %v", reqs)
	}
}

func TestDeriveHTTPRouteForwardsToParentGatewayProxy(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "kgateway",
		Annotations: map[string]string{AnnotationGatewayService: "kgateway/external:443"}}}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "kgateway"},
		Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.5"}}
	rtObj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "immich", Namespace: "immich"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{
				{Name: "external", Namespace: nsPtr("kgateway")}, // group/kind nil → defaults to Gateway
			}},
			Hostnames: []gwv1.Hostname{"immich.clerici.tech"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(gw, svc).Build()
	rt, ok, err := (&HTTPRouteSourceReconciler{Client: c}).deriveHTTPRouteRouting(context.Background(), rtObj, func(string, string) {})
	if err != nil || !ok {
		t.Fatalf("expected ok, got ok=%v err=%v", ok, err)
	}
	if len(rt.services) != 1 || rt.services[0]["hostname"] != "immich.clerici.tech" || rt.services[0]["origin"] != "10.0.0.5:443" {
		t.Fatalf("unexpected routing: %+v", rt.services)
	}
}

func TestDeriveHTTPRouteNilGroupKindResolves(t *testing.T) {
	// parentRef with nil Group AND nil Kind (and nil Namespace → defaults to route ns) must resolve as a Gateway parent.
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "immich",
		Annotations: map[string]string{AnnotationGatewayService: "immich/gw:443"}}}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "immich"},
		Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.9"}}
	rtObj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "immich"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "gw"}}}, // all nil
			Hostnames:       []gwv1.Hostname{"a.example"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(gw, svc).Build()
	rt, ok, err := (&HTTPRouteSourceReconciler{Client: c}).deriveHTTPRouteRouting(context.Background(), rtObj, func(string, string) {})
	if err != nil || !ok || len(rt.services) != 1 || rt.services[0]["origin"] != "10.0.0.9:443" {
		t.Fatalf("nil group/kind/ns must resolve: ok=%v err=%v rt=%+v", ok, err, rt.services)
	}
}

func TestDeriveHTTPRouteAmbiguousGateway(t *testing.T) {
	// two parentRefs → two Gateways with DIFFERENT gateway-service proxies → ReasonAmbiguousGateway, ok=false.
	gwA := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "gw-a", Namespace: "kgateway",
		Annotations: map[string]string{AnnotationGatewayService: "kgateway/gw-a:443"}}}
	gwB := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "gw-b", Namespace: "kgateway",
		Annotations: map[string]string{AnnotationGatewayService: "kgateway/gw-b:443"}}}
	svcA := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "gw-a", Namespace: "kgateway"}, Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.1"}}
	svcB := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "gw-b", Namespace: "kgateway"}, Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.2"}}
	rtObj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "immich"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{
				{Name: "gw-a", Namespace: nsPtr("kgateway")}, {Name: "gw-b", Namespace: nsPtr("kgateway")},
			}},
			Hostnames: []gwv1.Hostname{"a.example"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(gwA, gwB, svcA, svcB).Build()
	var reason string
	if _, ok, _ := (&HTTPRouteSourceReconciler{Client: c}).deriveHTTPRouteRouting(context.Background(), rtObj, func(r, _ string) { reason = r }); ok || reason != ReasonAmbiguousGateway {
		t.Fatalf("want AmbiguousGateway skip, got ok=%v reason=%q", ok, reason)
	}
}

func TestDeriveHTTPRoutePortlessGatewayServiceUsesFirstPort(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "kgateway",
		Annotations: map[string]string{AnnotationGatewayService: "kgateway/external"}}} // no :port
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "kgateway"},
		Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.5", Ports: []corev1.ServicePort{{Port: 8443}}}}
	rtObj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "immich"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "external", Namespace: nsPtr("kgateway")}}},
			Hostnames:       []gwv1.Hostname{"a.example"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(gw, svc).Build()
	rt, ok, err := (&HTTPRouteSourceReconciler{Client: c}).deriveHTTPRouteRouting(context.Background(), rtObj, func(string, string) {})
	if err != nil || !ok || len(rt.services) != 1 || rt.services[0]["origin"] != "10.0.0.5:8443" {
		t.Fatalf("port-less must resolve to first Service port: ok=%v err=%v rt=%+v", ok, err, rt.services)
	}
}

func TestDeriveHTTPRouteSkipsNonGatewayParent(t *testing.T) {
	kind := gwv1.Kind("Service")
	rtObj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "immich"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "x", Kind: &kind}}},
			Hostnames:       []gwv1.Hostname{"a.example"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
	var reason string
	if _, ok, _ := (&HTTPRouteSourceReconciler{Client: c}).deriveHTTPRouteRouting(context.Background(), rtObj, func(r, _ string) { reason = r }); ok || reason != ReasonGatewayServiceUnset {
		t.Fatalf("non-Gateway parent must be skipped: ok=%v reason=%q", ok, reason)
	}
}

func TestDeriveHTTPRouteGatewayServiceUnset(t *testing.T) {
	// parent Gateway exists but has no towonel.io/gateway-service → ReasonGatewayServiceUnset, ok=false.
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "kgateway"}}
	rtObj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "immich"},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "external", Namespace: nsPtr("kgateway")}}},
			Hostnames:       []gwv1.Hostname{"a.example"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(gw).Build()
	var reason string
	if _, ok, _ := (&HTTPRouteSourceReconciler{Client: c}).deriveHTTPRouteRouting(context.Background(), rtObj, func(r, _ string) { reason = r }); ok || reason != ReasonGatewayServiceUnset {
		t.Fatalf("want GatewayServiceUnset skip, got ok=%v reason=%q", ok, reason)
	}
}

func TestHTTPRouteSourcePredicateAdmitsUnannotatedWithGatewayParent(t *testing.T) {
	p := httpRouteSourcePredicate()

	// un-annotated route WITH a Gateway parentRef → admitted (may inherit auto-routes).
	withParent := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: "ns"},
		Spec:       gwv1.HTTPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "gw"}}}},
	}
	if !p.Create(event.CreateEvent{Object: withParent}) {
		t.Fatal("un-annotated route with a Gateway parentRef must be admitted")
	}

	// un-annotated route WITHOUT any parentRef → not admitted.
	noParent := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "r2", Namespace: "ns"}}
	if p.Create(event.CreateEvent{Object: noParent}) {
		t.Fatal("un-annotated route with no Gateway parentRef must NOT be admitted")
	}

	// un-annotated route whose only parentRef is a NON-Gateway → not admitted.
	svcKind := gwv1.Kind("Service")
	nonGw := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "r3", Namespace: "ns"},
		Spec:       gwv1.HTTPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "x", Kind: &svcKind}}}},
	}
	if p.Create(event.CreateEvent{Object: nonGw}) {
		t.Fatal("un-annotated route with only a non-Gateway parentRef must NOT be admitted")
	}

	// annotated route with NO parentRef → still admitted (annotation alone qualifies).
	annotated := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Name: "r4", Namespace: "ns",
		Annotations: map[string]string{AnnotationTunnel: "true"}}}
	if !p.Create(event.CreateEvent{Object: annotated}) {
		t.Fatal("annotated route must be admitted regardless of parentRefs")
	}
}

func TestAutoSelectedByGateway(t *testing.T) {
	const ns = "app"
	mkGW := func(ann map[string]string) *gwv1.Gateway {
		return &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: ns, Annotations: ann}}
	}
	mkRoute := func(routeNS string, p gwv1.ParentReference) *gwv1.HTTPRoute {
		return &gwv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "r", Namespace: routeNS},
			Spec:       gwv1.HTTPRouteSpec{CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{p}}},
		}
	}

	t.Run("enabled gateway with gateway-service selects same-ns route", func(t *testing.T) {
		gw := mkGW(map[string]string{AnnotationAutoRoutes: "true", AnnotationGatewayService: "envoy:443"})
		c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(gw).Build()
		r := &HTTPRouteSourceReconciler{Client: c}
		sel, err := r.autoSelectedByGateway(context.Background(), mkRoute(ns, gwv1.ParentReference{Name: "gw"}), func(string, string) {})
		if err != nil || !sel {
			t.Fatalf("want selected, got sel=%v err=%v", sel, err)
		}
	})

	t.Run("auto-routes false → not selected", func(t *testing.T) {
		gw := mkGW(map[string]string{AnnotationAutoRoutes: "false", AnnotationGatewayService: "envoy:443"})
		c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(gw).Build()
		r := &HTTPRouteSourceReconciler{Client: c}
		if sel, err := r.autoSelectedByGateway(context.Background(), mkRoute(ns, gwv1.ParentReference{Name: "gw"}), func(string, string) {}); sel || err != nil {
			t.Fatalf("want not selected, got sel=%v err=%v", sel, err)
		}
	})

	t.Run("enabled gateway WITHOUT gateway-service → no-op + event, not selected", func(t *testing.T) {
		gw := mkGW(map[string]string{AnnotationAutoRoutes: "true"})
		c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(gw).Build()
		r := &HTTPRouteSourceReconciler{Client: c}
		var reason string
		sel, err := r.autoSelectedByGateway(context.Background(), mkRoute(ns, gwv1.ParentReference{Name: "gw"}), func(rs, _ string) { reason = rs })
		if sel || err != nil {
			t.Fatalf("want not selected, got sel=%v err=%v", sel, err)
		}
		if reason != ReasonGatewayServiceUnset {
			t.Fatalf("want ReasonGatewayServiceUnset event, got %q", reason)
		}
	})

	t.Run("cross-namespace parentRef → not selected (namespace scoping)", func(t *testing.T) {
		gw := mkGW(map[string]string{AnnotationAutoRoutes: "true", AnnotationGatewayService: "envoy:443"})
		c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(gw).Build()
		r := &HTTPRouteSourceReconciler{Client: c}
		route := mkRoute("other", gwv1.ParentReference{Name: "gw", Namespace: nsPtr(ns)})
		if sel, err := r.autoSelectedByGateway(context.Background(), route, func(string, string) {}); sel || err != nil {
			t.Fatalf("cross-namespace route must NOT be auto-selected, got sel=%v err=%v", sel, err)
		}
	})

	t.Run("no Gateway parent → not selected", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
		r := &HTTPRouteSourceReconciler{Client: c}
		svcKind := gwv1.Kind("Service")
		route := mkRoute(ns, gwv1.ParentReference{Name: "x", Kind: &svcKind})
		if sel, err := r.autoSelectedByGateway(context.Background(), route, func(string, string) {}); sel || err != nil {
			t.Fatalf("want not selected, got sel=%v err=%v", sel, err)
		}
	})
}
