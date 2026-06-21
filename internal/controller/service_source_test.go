package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func TestSourcesForAgentServiceMatchesByAgentRefAndNamespace(t *testing.T) {
	agent := &towonelv1alpha1.TowonelAgent{}
	agent.Namespace, agent.Name = "app", "my-agent"
	mk := func(name string, ann map[string]string) *corev1.Service {
		return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "app", Annotations: ann}}
	}
	// A: agent-ref==my-agent, bare tunnel-ref → ns "app" == agent ns → MUST match
	svcA := mk("a", map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "t", AnnotationAgentRef: "my-agent"})
	// B: different agent-ref → MUST NOT match
	svcB := mk("b", map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "t", AnnotationAgentRef: "other-agent"})
	// C: no agent-ref → MUST NOT match
	svcC := mk("c", map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "t"})
	// D: agent-ref==my-agent but tunnel-ref ns "other" != agent ns → MUST NOT match
	svcD := mk("d", map[string]string{AnnotationTunnel: "enable", AnnotationTunnelRef: "other/t", AnnotationAgentRef: "my-agent"})
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(svcA, svcB, svcC, svcD).Build()
	reqs := (&ServiceSourceReconciler{Client: c}).sourcesForAgent(context.Background(), agent)
	if len(reqs) != 1 || reqs[0].NamespacedName.String() != "app/a" {
		t.Fatalf("want exactly {app/a}, got %v", reqs)
	}
}

func svcWith(ann map[string]string, ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "net", Name: "app", Annotations: ann},
		Spec:       corev1.ServiceSpec{ClusterIP: "10.0.0.1", Ports: ports},
	}
}

func TestDeriveServiceRoutingSingleHTTPS(t *testing.T) {
	svc := svcWith(map[string]string{AnnotationSrcHostname: "app.example"}, corev1.ServicePort{Port: 8080})
	rt, ok := (&ServiceSourceReconciler{}).deriveServiceRouting(svc, func(string, string) {})
	if !ok || len(rt.services) != 1 || rt.services[0]["hostname"] != "app.example" || rt.services[0]["origin"] != "10.0.0.1:8080" {
		t.Fatalf("ok=%v rt=%+v", ok, rt)
	}
}

func TestDeriveServiceRoutingPortScoped(t *testing.T) {
	svc := svcWith(map[string]string{
		"towonel.io/web.hostname":     "app.example",
		"towonel.io/game.tcp":         "true",
		"towonel.io/game.public-port": "4086",
	}, corev1.ServicePort{Name: "web", Port: 8080}, corev1.ServicePort{Name: "game", Port: 4086})
	rt, ok := (&ServiceSourceReconciler{}).deriveServiceRouting(svc, func(string, string) {})
	if !ok || len(rt.services) != 1 || len(rt.tcp) != 1 {
		t.Fatalf("ok=%v rt=%+v", ok, rt)
	}
	if rt.tcp[0]["name"] != "game" || rt.tcp[0]["origin"] != "10.0.0.1:4086" || rt.tcp[0]["preferredPort"] != int64(4086) {
		t.Fatalf("tcp entry wrong: %+v", rt.tcp[0])
	}
}

func TestDeriveServiceRoutingMissingPortEvents(t *testing.T) {
	svc := svcWith(map[string]string{"towonel.io/ghost.hostname": "x.example"}, corev1.ServicePort{Name: "web", Port: 80})
	var reason string
	_, ok := (&ServiceSourceReconciler{}).deriveServiceRouting(svc, func(r, _ string) { reason = r })
	if ok || reason != ReasonInvalidAnnotation {
		t.Fatalf("ok=%v reason=%q", ok, reason)
	}
}
