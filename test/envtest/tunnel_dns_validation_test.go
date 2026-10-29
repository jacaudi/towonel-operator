package envtest_test

import (
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// TestTunnelDNSSpecValidation pins the CRD markers of design §5.1.
func TestTunnelDNSSpecValidation(t *testing.T) {
	ctx := t.Context()
	ns := mustNamespace(t)
	mk := func(name string, dns towonelv1alpha1.TunnelDNSSpec) *towonelv1alpha1.TowonelTunnel {
		return &towonelv1alpha1.TowonelTunnel{
			Name: name, Namespace: ns,
			Spec: towonelv1alpha1.TowonelTunnelSpec{DNS: dns},
		}
	}
	tooMany := make([]towonelv1alpha1.DNSProviderProperty, 101)
	for i := range tooMany {
		tooMany[i] = towonelv1alpha1.DNSProviderProperty{Name: fmt.Sprintf("p%d", i)}
	}
	accept := map[string]towonelv1alpha1.TunnelDNSSpec{
		"ok-full": {
			Mode: towonelv1alpha1.DNSModeDNSEndpoint, CNAMETarget: "edge-na.towonel.dev", Namespace: "network",
			IncludeWildcards: true, Labels: map[string]string{"example.com/dns-endpoint": "public"},
			Annotations:      map[string]string{"note": "x"},
			ProviderSpecific: []towonelv1alpha1.DNSProviderProperty{{Name: "cloudflare/proxied", Value: "false"}},
		},
		"ok-trailing-dot": {Mode: towonelv1alpha1.DNSModeDNSEndpoint, CNAMETarget: "edge-na.towonel.dev."},
		"ok-defaults":     {Mode: towonelv1alpha1.DNSModeDNSEndpoint},
	}
	for name, dns := range accept {
		if err := k8sClient.Create(ctx, mk(name, dns)); err != nil {
			t.Errorf("%s: create rejected: %v", name, err)
		}
	}
	var got towonelv1alpha1.TowonelTunnel
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "ok-defaults"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.DNS.DefaultTTL != 300 {
		t.Errorf("defaultTTL default = %d, want 300", got.Spec.DNS.DefaultTTL)
	}
	reject := map[string]towonelv1alpha1.TunnelDNSSpec{
		"bad-target-upper":        {CNAMETarget: "Edge.Example.com"},
		"bad-target-single-label": {CNAMETarget: "localhost"},
		"bad-target-long-label":   {CNAMETarget: strings.Repeat("a", 64) + ".example.com"},
		"bad-namespace":           {Namespace: "Bad_NS"},
		"bad-ttl-negative":        {DefaultTTL: -1},
		"bad-provider-empty-name": {ProviderSpecific: []towonelv1alpha1.DNSProviderProperty{{Name: ""}}},
		"bad-provider-too-many":   {ProviderSpecific: tooMany},
	}
	for name, dns := range reject {
		err := k8sClient.Create(ctx, mk(name, dns))
		if !apierrors.IsInvalid(err) {
			t.Errorf("%s: create err = %v, want Invalid", name, err)
		}
	}
}
