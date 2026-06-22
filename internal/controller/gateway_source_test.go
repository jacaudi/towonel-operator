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

func hn(s string) *gwv1.Hostname { h := gwv1.Hostname(s); return &h }

func TestSourcesForAgentGatewayMatchesByAgentRefAndNamespace(t *testing.T) {
	agent := &towonelv1alpha1.TowonelAgent{}
	agent.Namespace, agent.Name = "app", "my-agent"
	mk := func(name string, ann map[string]string) *gwv1.Gateway {
		return &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app", Annotations: ann}}
	}
	// A: agent-ref==my-agent, bare tunnel-ref → ns "app" == agent ns → MUST match
	gwA := mk("a", map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "t", AnnotationAgentRef: "my-agent"})
	// B: different agent-ref → MUST NOT match
	gwB := mk("b", map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "t", AnnotationAgentRef: "other-agent"})
	// C: no agent-ref → MUST NOT match
	gwC := mk("c", map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "t"})
	// D: agent-ref==my-agent but tunnel-ref ns "other" != agent ns → MUST NOT match
	gwD := mk("d", map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "other/t", AnnotationAgentRef: "my-agent"})
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(gwA, gwB, gwC, gwD).Build()
	reqs := (&GatewaySourceReconciler{Client: c}).sourcesForAgent(context.Background(), agent)
	if len(reqs) != 1 || reqs[0].NamespacedName.String() != "app/a" {
		t.Fatalf("want exactly {app/a}, got %v", reqs)
	}
}

func TestDeriveGatewayRoutingForwardsToProxy(t *testing.T) {
	proxy := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "infra", Name: "envoy"},
		Spec:       corev1.ServiceSpec{ClusterIP: "10.1.2.3", Ports: []corev1.ServicePort{{Port: 443}}},
	}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(proxy).Build()
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "net", Name: "gw", Annotations: map[string]string{
			AnnotationGatewayService: "infra/envoy:443",
		}},
		Spec: gwv1.GatewaySpec{Listeners: []gwv1.Listener{
			{Name: "https", Hostname: hn("a.example")},
			{Name: "https2", Hostname: hn("b.example")},
		}},
	}
	rt, ok := deriveGatewayRouting(context.Background(), c, gw, func(string, string) {})
	if !ok || len(rt.services) != 2 {
		t.Fatalf("ok=%v rt=%+v", ok, rt)
	}
	for _, s := range rt.services {
		if s["origin"] != "10.1.2.3:443" {
			t.Fatalf("origin should be the proxy Service: %+v", s)
		}
	}
}

func TestDeriveGatewayRoutingRequiresAnnotation(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "net", Name: "gw"}}
	var reason string
	if _, ok := deriveGatewayRouting(context.Background(), c, gw, func(r, _ string) { reason = r }); ok || reason != ReasonGatewayServiceUnset {
		t.Fatalf("ok=%v reason=%q", ok, reason)
	}
}
