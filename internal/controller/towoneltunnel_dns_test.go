package controller

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	externaldnsv1alpha1 "sigs.k8s.io/external-dns/apis/v1alpha1"
	"sigs.k8s.io/external-dns/endpoint"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func dnsTunnel(dns towonelv1alpha1.TunnelDNSSpec) *towonelv1alpha1.TowonelTunnel {
	return &towonelv1alpha1.TowonelTunnel{
		Name: "external", Namespace: "network",
		Spec: towonelv1alpha1.TowonelTunnelSpec{Region: "CA", DNS: dns},
	}
}

var testTargets = map[string]string{"CA": "edge-na.example.com", "EU": "edge-eu.example.com"}

func TestDesiredDNSEndpointTargetPrecedence(t *testing.T) {
	view := inviteView{Confirmed: []string{"a.example.com"}, Region: "EU"}
	// Override wins (trailing dot trimmed).
	obj, _, err := desiredDNSEndpoint(dnsTunnel(towonelv1alpha1.TunnelDNSSpec{CNAMETarget: "custom.example.com."}), view, testTargets)
	if err != nil || obj.Spec.Endpoints[0].Targets[0] != "custom.example.com" {
		t.Fatalf("override: err=%v target=%v, want custom.example.com", err, obj)
	}
	// Map keyed on the HUB region: spec.region is CA but the invite is EU.
	obj, _, err = desiredDNSEndpoint(dnsTunnel(towonelv1alpha1.TunnelDNSSpec{}), view, testTargets)
	if err != nil || obj.Spec.Endpoints[0].Targets[0] != "edge-eu.example.com" {
		t.Fatalf("map: err=%v obj=%v, want edge-eu.example.com (hub region, not spec.region)", err, obj)
	}
	// Neither → errNoEdgeTarget.
	if _, _, err := desiredDNSEndpoint(dnsTunnel(towonelv1alpha1.TunnelDNSSpec{}), inviteView{Region: "AP"}, testTargets); !errors.Is(err, errNoEdgeTarget) {
		t.Fatalf("err = %v, want errNoEdgeTarget", err)
	}
}

func TestDesiredDNSEndpointEndpoints(t *testing.T) {
	dns := towonelv1alpha1.TunnelDNSSpec{
		DefaultTTL:       300,
		ProviderSpecific: []towonelv1alpha1.DNSProviderProperty{{Name: "cloudflare/proxied", Value: "false"}},
	}
	view := inviteView{Confirmed: []string{"b.example.com", "*.example.com", "a.example.com", "a.example.com"}, Region: "CA"}
	obj, records, err := desiredDNSEndpoint(dnsTunnel(dns), view, testTargets)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, ep := range obj.Spec.Endpoints {
		names = append(names, ep.DNSName)
		if ep.RecordType != endpoint.RecordTypeCNAME || !slices.Equal(ep.Targets, endpoint.Targets{"edge-na.example.com"}) || ep.RecordTTL != 300 {
			t.Errorf("endpoint %+v, want CNAME → edge-na.example.com TTL 300", ep)
		}
		if len(ep.ProviderSpecific) != 1 || ep.ProviderSpecific[0] != (endpoint.ProviderSpecificProperty{Name: "cloudflare/proxied", Value: "false"}) {
			t.Errorf("providerSpecific = %v", ep.ProviderSpecific)
		}
	}
	if want := []string{"a.example.com", "b.example.com"}; !slices.Equal(names, want) {
		t.Fatalf("endpoints = %v, want %v (sorted, deduped, wildcard skipped)", names, want)
	}
	obj.Spec.Endpoints[0].ProviderSpecific[0].Value = "true"
	if obj.Spec.Endpoints[1].ProviderSpecific[0].Value != "false" {
		t.Fatal("providerSpecific slices are shared between endpoints")
	}
	if len(records) != 2 || records[0] != (towonelv1alpha1.DNSRecord{Hostname: "a.example.com", Type: "CNAME", Target: "edge-na.example.com"}) {
		t.Fatalf("records = %+v, want the endpoints projection", records)
	}
	dns.IncludeWildcards = true
	obj, _, _ = desiredDNSEndpoint(dnsTunnel(dns), view, testTargets)
	if len(obj.Spec.Endpoints) != 3 || obj.Spec.Endpoints[0].DNSName != "*.example.com" {
		t.Fatalf("includeWildcards: endpoints = %v, want *.example.com first of 3", obj.Spec.Endpoints)
	}
}

func TestDesiredDNSEndpointRevokedEmitsNothing(t *testing.T) {
	obj, records, err := desiredDNSEndpoint(dnsTunnel(towonelv1alpha1.TunnelDNSSpec{}),
		inviteView{Confirmed: []string{"a.example.com"}, Region: "CA", Revoked: true}, testTargets)
	if err != nil || len(obj.Spec.Endpoints) != 0 || len(records) != 0 {
		t.Fatalf("revoked: err=%v endpoints=%v records=%v, want none", err, obj.Spec.Endpoints, records)
	}
}

func TestDesiredDNSEndpointMetadata(t *testing.T) {
	dns := towonelv1alpha1.TunnelDNSSpec{
		Labels:      map[string]string{LabelOwnerTunnel: "spoof", "example.com/dns-endpoint": "public"},
		Annotations: map[string]string{AnnotationOwnerTunnelRef: "spoof", "note": "x"},
	}
	tt := dnsTunnel(dns)
	obj, _, err := desiredDNSEndpoint(tt, inviteView{Confirmed: []string{"a.example.com"}, Region: "CA"}, testTargets)
	if err != nil {
		t.Fatal(err)
	}
	if obj.APIVersion != "externaldns.k8s.io/v1alpha1" || obj.Kind != "DNSEndpoint" {
		t.Errorf("TypeMeta = %+v", obj.TypeMeta)
	}
	if obj.Name != "towonel.network.external" || obj.Namespace != "network" {
		t.Errorf("name = %s/%s, want network/towonel.network.external", obj.Namespace, obj.Name)
	}
	if obj.Labels[LabelOwnerTunnel] != ownerHash("network", "external") || obj.Labels[LabelManagedBy] != ManagedByValue ||
		obj.Labels["example.com/dns-endpoint"] != "public" {
		t.Errorf("labels = %v, want operator labels winning plus the user label", obj.Labels)
	}
	if obj.Annotations[AnnotationOwnerTunnelRef] != "network/external" || obj.Annotations["note"] != "x" {
		t.Errorf("annotations = %v", obj.Annotations)
	}
	if len(obj.OwnerReferences) != 0 {
		t.Errorf("ownerReferences = %v, want none (design §6.3)", obj.OwnerReferences)
	}
	if tt.Spec.DNS.Labels[LabelOwnerTunnel] != "spoof" || tt.Spec.DNS.Annotations[AnnotationOwnerTunnelRef] != "spoof" {
		t.Error("desiredDNSEndpoint mutated the tunnel's spec maps")
	}
	tt.Spec.DNS.Namespace = "dns"
	obj, _, _ = desiredDNSEndpoint(tt, inviteView{Confirmed: []string{"a.example.com"}, Region: "CA"}, testTargets)
	if obj.Namespace != "dns" || obj.Name != "towonel.network.external" {
		t.Errorf("spec.dns.namespace: got %s/%s, want dns/towonel.network.external", obj.Namespace, obj.Name)
	}
}

func TestDNSEndpointNameAndOwnerHash(t *testing.T) {
	if got := dnsEndpointName("default", "a.b"); got != "towonel.default.a.b" {
		t.Errorf("dotted name = %q", got)
	}
	long := strings.Repeat("x", 253)
	if got := dnsEndpointName("default", long); got != "towonel."+ownerHash("default", long) || len(got) != 24 {
		t.Errorf("over-length name = %q (len %d), want towonel.<16-hex hash>", got, len(got))
	}
	if h := ownerHash("network", "external"); len(h) != 16 || h != ownerHash("network", "external") {
		t.Errorf("ownerHash not a stable 16-char hex: %q", h)
	}
	if ownerHash("a-b", "c") == ownerHash("a", "b-c") {
		t.Error("ownerHash collides for a-b/c and a/b-c")
	}
}

// Disabled mode with the DNSEndpoint CRD absent (NoMatch on the sweep's List)
// is a clean no-op: reconcileDNS must NOT ask for dnsRetryRequeue (gate 2 B1,
// spec §8) and must leave no DNSEndpointSynced condition. The envtest cannot
// pin this: a 2-minute requeue loop outlasts its wait.
func TestReconcileDNSDisabledNoMatchDoesNotRetry(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddExternalDNSToScheme(s); err != nil {
		t.Fatal(err)
	}
	noMatch := &meta.NoKindMatchError{
		GroupKind:        schema.GroupKind{Group: externaldnsv1alpha1.GroupVersion.Group, Kind: "DNSEndpoint"},
		SearchedVersions: []string{externaldnsv1alpha1.GroupVersion.Version},
	}
	var lists int
	reader := interceptor.NewClient(fake.NewClientBuilder().WithScheme(s).Build(), interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			lists++
			return noMatch
		},
	})
	r := &TowonelTunnelReconciler{APIReader: reader}
	tt := dnsTunnel(towonelv1alpha1.TunnelDNSSpec{Mode: towonelv1alpha1.DNSModeDisabled})

	if retry := r.reconcileDNS(t.Context(), tt, inviteView{Confirmed: []string{"a.example.com"}, Region: "EU"}); retry {
		t.Fatal("reconcileDNS(Disabled, CRD absent) = retry true, want false (no requeue loop)")
	}
	if lists == 0 {
		t.Fatal("the sweep never listed DNSEndpoints; the test is not exercising the NoMatch path")
	}
	if c := meta.FindStatusCondition(tt.Status.Conditions, CondDNSEndpointSynced); c != nil {
		t.Fatalf("DNSEndpointSynced = %+v, want unset", c)
	}
}
