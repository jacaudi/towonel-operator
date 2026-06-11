package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func backend(name string, port int32) gwv1.HTTPBackendRef {
	p := gwv1.PortNumber(port)
	return gwv1.HTTPBackendRef{BackendRef: gwv1.BackendRef{BackendObjectReference: gwv1.BackendObjectReference{
		Name: gwv1.ObjectName(name), Port: &p,
	}}}
}

func TestDeriveHTTPRouteSingleBackend(t *testing.T) {
	be := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "net", Name: "app"}, Spec: corev1.ServiceSpec{ClusterIP: "10.2.0.1", Ports: []corev1.ServicePort{{Port: 8080}}}}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(be).Build()
	rt_obj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "net", Name: "r"},
		Spec: gwv1.HTTPRouteSpec{
			Hostnames: []gwv1.Hostname{"a.example", "b.example"},
			Rules:     []gwv1.HTTPRouteRule{{BackendRefs: []gwv1.HTTPBackendRef{backend("app", 8080)}}},
		},
	}
	rt, ok, err := (&HTTPRouteSourceReconciler{Client: c}).deriveHTTPRouteRouting(context.Background(), rt_obj, func(string, string) {})
	if err != nil || !ok || len(rt.services) != 2 || rt.services[0]["origin"] != "10.2.0.1:8080" {
		t.Fatalf("ok=%v err=%v rt=%+v", ok, err, rt)
	}
}

func TestDeriveHTTPRouteAmbiguousBackend(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
	rt_obj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "net", Name: "r"},
		Spec: gwv1.HTTPRouteSpec{
			Hostnames: []gwv1.Hostname{"a.example"},
			Rules:     []gwv1.HTTPRouteRule{{BackendRefs: []gwv1.HTTPBackendRef{backend("app", 8080), backend("other", 9090)}}},
		},
	}
	var reason string
	if _, ok, _ := (&HTTPRouteSourceReconciler{Client: c}).deriveHTTPRouteRouting(context.Background(), rt_obj, func(r, _ string) { reason = r }); ok || reason != ReasonAmbiguousBackend {
		t.Fatalf("ok=%v reason=%q", ok, reason)
	}
}
