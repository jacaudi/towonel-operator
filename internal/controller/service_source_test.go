package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
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

// reflessSvc is a tunnel-opted Service with NO towonel.io/tunnel-ref, exposing a
// single HTTPS hostname over its ClusterIP:port — exercises the omission default.
func reflessSvc() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "net", Name: "app", Annotations: map[string]string{
			AnnotationTunnel:      "enable",
			AnnotationSrcHostname: "app.example",
		}},
		Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.1", Ports: []corev1.ServicePort{{Port: 8080}}},
	}
}

func mkTunnel(ns, name string) *towonelv1alpha1.TowonelTunnel {
	tt := &towonelv1alpha1.TowonelTunnel{}
	tt.Namespace, tt.Name = ns, name
	return tt
}

func TestServiceReconcileRefLessSingleTunnelContributes(t *testing.T) {
	svc := reflessSvc()
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).
		WithObjects(svc, mkTunnel("net", "only")).Build()
	rec := record.NewFakeRecorder(8)
	r := &ServiceSourceReconciler{Client: c, APIReader: c, Recorder: rec}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "net", Name: "app"}}); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	// The omission default resolved tunnel net/only and (no agent-ref) minted the
	// operator-owned default agent for it.
	var agents towonelv1alpha1.TowonelAgentList
	if err := c.List(context.Background(), &agents); err != nil {
		t.Fatal(err)
	}
	if len(agents.Items) != 1 {
		t.Fatalf("want exactly one default agent, got %d", len(agents.Items))
	}
	want := defaultAgentName("net", "only")
	if agents.Items[0].Name != want {
		t.Fatalf("agent name = %q, want %q", agents.Items[0].Name, want)
	}
	for {
		select {
		case ev := <-rec.Events:
			if strings.Contains(ev, ReasonTunnelRefMissing) {
				t.Fatalf("unexpected TunnelRefMissing event: %q", ev)
			}
		default:
			return
		}
	}
}

func TestServiceReconcileRefLessMultiTunnelSkipsLoudly(t *testing.T) {
	svc := reflessSvc()
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).
		WithObjects(svc, mkTunnel("net", "a"), mkTunnel("net", "b")).Build()
	rec := record.NewFakeRecorder(8)
	r := &ServiceSourceReconciler{Client: c, APIReader: c, Recorder: rec}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "net", Name: "app"}}); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	// Ambiguous: no agent contributed.
	var agents towonelv1alpha1.TowonelAgentList
	if err := c.List(context.Background(), &agents); err != nil {
		t.Fatal(err)
	}
	if len(agents.Items) != 0 {
		t.Fatalf("ambiguous ref-less source must not contribute; found %d agents", len(agents.Items))
	}
	var sawMissing bool
	for {
		select {
		case ev := <-rec.Events:
			if strings.Contains(ev, ReasonTunnelRefMissing) {
				sawMissing = true
			}
		default:
			if !sawMissing {
				t.Fatal("want a TunnelRefMissing event for the ambiguous ref-less source")
			}
			return
		}
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
