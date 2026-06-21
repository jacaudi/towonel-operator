package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
