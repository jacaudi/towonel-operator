package envtest_test

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	externaldnsv1alpha1 "sigs.k8s.io/external-dns/apis/v1alpha1"
	"sigs.k8s.io/external-dns/endpoint"
)

// TestDNSEndpointAPIRoundTrip is the gate-B1 regression: SSA-create, label
// List and Delete of a typed DNSEndpoint must work against a real API server.
func TestDNSEndpointAPIRoundTrip(t *testing.T) {
	ctx := t.Context()
	ns := mustNamespace(t)
	obj := &externaldnsv1alpha1.DNSEndpoint{
		APIVersion: externaldnsv1alpha1.GroupVersion.String(), Kind: "DNSEndpoint",
		Name: "roundtrip", Namespace: ns, Labels: map[string]string{"t": "roundtrip"},
		Spec: externaldnsv1alpha1.DNSEndpointSpec{Endpoints: []*endpoint.Endpoint{{
			DNSName: "a.example.com", RecordType: endpoint.RecordTypeCNAME,
			Targets: endpoint.Targets{"edge.example.com"}, RecordTTL: 300,
		}}},
	}
	if err := k8sClient.Patch(ctx, obj, client.Apply, client.FieldOwner("roundtrip-test"), client.ForceOwnership); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var list externaldnsv1alpha1.DNSEndpointList
	if err := k8sClient.List(ctx, &list, client.InNamespace(ns), client.MatchingLabels{"t": "roundtrip"}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Items) != 1 || len(list.Items[0].Spec.Endpoints) != 1 || list.Items[0].Spec.Endpoints[0].DNSName != "a.example.com" {
		t.Fatalf("list = %+v, want one object with a.example.com", list.Items)
	}
	if err := k8sClient.Delete(ctx, &list.Items[0]); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
