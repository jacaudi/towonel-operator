package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func hn(s string) *gwv1.Hostname { h := gwv1.Hostname(s); return &h }

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
