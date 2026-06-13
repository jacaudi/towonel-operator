package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
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
	rtObj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "net", Name: "r"},
		Spec: gwv1.HTTPRouteSpec{
			Hostnames: []gwv1.Hostname{"a.example", "b.example"},
			Rules:     []gwv1.HTTPRouteRule{{BackendRefs: []gwv1.HTTPBackendRef{backend("app", 8080)}}},
		},
	}
	rt, ok, err := (&HTTPRouteSourceReconciler{Client: c}).deriveHTTPRouteRouting(context.Background(), rtObj, func(string, string) {})
	if err != nil || !ok || len(rt.services) != 2 || rt.services[0]["origin"] != "10.2.0.1:8080" {
		t.Fatalf("ok=%v err=%v rt=%+v", ok, err, rt)
	}
}

func TestDeriveHTTPRouteAmbiguousBackend(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).Build()
	rtObj := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "net", Name: "r"},
		Spec: gwv1.HTTPRouteSpec{
			Hostnames: []gwv1.Hostname{"a.example"},
			Rules:     []gwv1.HTTPRouteRule{{BackendRefs: []gwv1.HTTPBackendRef{backend("app", 8080), backend("other", 9090)}}},
		},
	}
	var reason string
	if _, ok, _ := (&HTTPRouteSourceReconciler{Client: c}).deriveHTTPRouteRouting(context.Background(), rtObj, func(r, _ string) { reason = r }); ok || reason != ReasonAmbiguousBackend {
		t.Fatalf("ok=%v reason=%q", ok, reason)
	}
}

func makeGrant(backendNS, routeNS, backendName string, nameAll bool) *gwv1beta1.ReferenceGrant {
	to := gwv1.ReferenceGrantTo{Group: "", Kind: "Service"}
	if !nameAll {
		n := gwv1.ObjectName(backendName)
		to.Name = &n
	}
	g := gwv1beta1.ReferenceGrant(gwv1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{Namespace: backendNS, Name: "grant"},
		Spec: gwv1.ReferenceGrantSpec{
			From: []gwv1.ReferenceGrantFrom{{
				Group:     "gateway.networking.k8s.io",
				Kind:      "HTTPRoute",
				Namespace: gwv1.Namespace(routeNS),
			}},
			To: []gwv1.ReferenceGrantTo{to},
		},
	})
	return &g
}

func TestReferenceGrantAllows(t *testing.T) {
	cases := []struct {
		name        string
		grants      []gwv1beta1.ReferenceGrant
		backendNS   string
		routeNS     string
		backendName string
		want        bool
	}{
		{
			name:        "named grant allows",
			grants:      []gwv1beta1.ReferenceGrant{*makeGrant("backend", "route", "svc", false)},
			backendNS:   "backend",
			routeNS:     "route",
			backendName: "svc",
			want:        true,
		},
		{
			name:        "nil name (all Services) allows",
			grants:      []gwv1beta1.ReferenceGrant{*makeGrant("backend", "route", "", true)},
			backendNS:   "backend",
			routeNS:     "route",
			backendName: "svc",
			want:        true,
		},
		{
			name:        "no matching grant denies",
			grants:      nil,
			backendNS:   "backend",
			routeNS:     "route",
			backendName: "svc",
			want:        false,
		},
		{
			name:        "grant for different route namespace denies",
			grants:      []gwv1beta1.ReferenceGrant{*makeGrant("backend", "other-route", "svc", false)},
			backendNS:   "backend",
			routeNS:     "route",
			backendName: "svc",
			want:        false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			objs := make([]interface{}, len(tc.grants))
			for i := range tc.grants {
				objs[i] = &tc.grants[i]
			}
			b := fake.NewClientBuilder().WithScheme(srcScheme(t))
			for i := range tc.grants {
				b = b.WithObjects(&tc.grants[i])
			}
			c := b.Build()
			got, err := referenceGrantAllows(context.Background(), c, tc.backendNS, tc.routeNS, tc.backendName)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("referenceGrantAllows = %v, want %v", got, tc.want)
			}
		})
	}
}
