package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	externaldnsv1alpha1 "sigs.k8s.io/external-dns/apis/v1alpha1"
)

// TestAddExternalDNSToSchemeRegistersOptions pins design §6.6 / gate B1:
// upstream AddToScheme registers only the types, so without
// metav1.AddToGroupVersion the client cannot convert List/Patch/Create options
// for externaldns.k8s.io/v1alpha1 against a real API server.
func TestAddExternalDNSToSchemeRegistersOptions(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddExternalDNSToScheme(s); err != nil {
		t.Fatalf("AddExternalDNSToScheme: %v", err)
	}
	gv := externaldnsv1alpha1.GroupVersion
	for _, kind := range []string{"DNSEndpoint", "DNSEndpointList", "ListOptions", "GetOptions", "CreateOptions", "PatchOptions", "DeleteOptions"} {
		if !s.Recognizes(gv.WithKind(kind)) {
			t.Errorf("scheme does not recognize %s", gv.WithKind(kind))
		}
	}
}
