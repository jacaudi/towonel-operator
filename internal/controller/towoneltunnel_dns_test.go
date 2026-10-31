package controller

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
	obj, _, err := desiredDNSEndpoint(dnsTunnel(towonelv1alpha1.TunnelDNSSpec{CNAMETarget: "custom.example.com."}), view, testTargets, nil)
	if err != nil || obj.Spec.Endpoints[0].Targets[0] != "custom.example.com" {
		t.Fatalf("override: err=%v target=%v, want custom.example.com", err, obj)
	}
	// Map keyed on the HUB region: spec.region is CA but the invite is EU.
	obj, _, err = desiredDNSEndpoint(dnsTunnel(towonelv1alpha1.TunnelDNSSpec{}), view, testTargets, nil)
	if err != nil || obj.Spec.Endpoints[0].Targets[0] != "edge-eu.example.com" {
		t.Fatalf("map: err=%v obj=%v, want edge-eu.example.com (hub region, not spec.region)", err, obj)
	}
	// Neither → errNoEdgeTarget.
	if _, _, err := desiredDNSEndpoint(dnsTunnel(towonelv1alpha1.TunnelDNSSpec{}), inviteView{Region: "AP"}, testTargets, nil); !errors.Is(err, errNoEdgeTarget) {
		t.Fatalf("err = %v, want errNoEdgeTarget", err)
	}
}

func TestDesiredDNSEndpointEndpoints(t *testing.T) {
	dns := towonelv1alpha1.TunnelDNSSpec{
		DefaultTTL:       300,
		ProviderSpecific: []towonelv1alpha1.DNSProviderProperty{{Name: "cloudflare/proxied", Value: "false"}},
	}
	view := inviteView{Confirmed: []string{"b.example.com", "*.example.com", "a.example.com", "a.example.com"}, Region: "CA"}
	obj, records, err := desiredDNSEndpoint(dnsTunnel(dns), view, testTargets, nil)
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
	obj, _, _ = desiredDNSEndpoint(dnsTunnel(dns), view, testTargets, nil)
	if len(obj.Spec.Endpoints) != 3 || obj.Spec.Endpoints[0].DNSName != "*.example.com" {
		t.Fatalf("includeWildcards: endpoints = %v, want *.example.com first of 3", obj.Spec.Endpoints)
	}
}

func TestDesiredDNSEndpointRevokedEmitsNothing(t *testing.T) {
	obj, records, err := desiredDNSEndpoint(dnsTunnel(towonelv1alpha1.TunnelDNSSpec{}),
		inviteView{Confirmed: []string{"a.example.com"}, Region: "CA", Revoked: true}, testTargets, nil)
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
	obj, _, err := desiredDNSEndpoint(tt, inviteView{Confirmed: []string{"a.example.com"}, Region: "CA"}, testTargets, nil)
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
	obj, _, _ = desiredDNSEndpoint(tt, inviteView{Confirmed: []string{"a.example.com"}, Region: "CA"}, testTargets, nil)
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

	if retry := r.reconcileDNS(t.Context(), tt, inviteView{Confirmed: []string{"a.example.com"}, Region: "EU"}, nil); retry {
		t.Fatal("reconcileDNS(Disabled, CRD absent) = retry true, want false (no requeue loop)")
	}
	if lists == 0 {
		t.Fatal("the sweep never listed DNSEndpoints; the test is not exercising the NoMatch path")
	}
	if c := meta.FindStatusCondition(tt.Status.Conditions, CondDNSEndpointSynced); c != nil {
		t.Fatalf("DNSEndpointSynced = %+v, want unset", c)
	}
}

// A Forbidden on the pre-delete sweep must keep the finalizer: failing closed
// beats orphaning public DNS records (design §6.5).
func TestReconcileDeleteSweepForbiddenKeepsFinalizer(t *testing.T) {
	s := runtime.NewScheme()
	if err := towonelv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := AddExternalDNSToScheme(s); err != nil {
		t.Fatal(err)
	}
	now := metav1.Now()
	tt := dnsTunnel(towonelv1alpha1.TunnelDNSSpec{Mode: towonelv1alpha1.DNSModeDNSEndpoint})
	tt.DeletionTimestamp = &now
	tt.Finalizers = []string{FinalizerName}
	tt.Spec.DeletionPolicy = towonelv1alpha1.DeletionPolicyDelete

	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Group: externaldnsv1alpha1.GroupVersion.Group, Resource: "dnsendpoints"}, "", errors.New("rbac"))
	c := interceptor.NewClient(fake.NewClientBuilder().WithScheme(s).WithObjects(tt).Build(), interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return forbidden
		},
	})
	r := &TowonelTunnelReconciler{Client: c, APIReader: c}

	if _, err := r.reconcileDelete(t.Context(), tt); !apierrors.IsForbidden(err) {
		t.Fatalf("reconcileDelete err = %v, want Forbidden", err)
	}
	if !controllerutil.ContainsFinalizer(tt, FinalizerName) {
		t.Fatal("finalizer removed in memory despite a failed DNS sweep")
	}
	var stored towonelv1alpha1.TowonelTunnel
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(tt), &stored); err != nil {
		t.Fatal(err)
	}
	if !controllerutil.ContainsFinalizer(&stored, FinalizerName) {
		t.Fatal("finalizer removed from the stored tunnel despite a failed DNS sweep")
	}
}

func TestDesiredDNSEndpointOptOut(t *testing.T) {
	view := inviteView{Confirmed: []string{"apex.example.com", "a.example.com", "b.example.com"}, Region: "CA"}
	tests := []struct {
		name   string
		optOut map[string]bool
		want   []string
	}{
		{"none", nil, []string{"a.example.com", "apex.example.com", "b.example.com"}},
		{"one excluded", map[string]bool{"apex.example.com": true}, []string{"a.example.com", "b.example.com"}},
		{"unrelated name ignored", map[string]bool{"other.example.com": true}, []string{"a.example.com", "apex.example.com", "b.example.com"}},
		{"all excluded", map[string]bool{"apex.example.com": true, "a.example.com": true, "b.example.com": true}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			obj, records, err := desiredDNSEndpoint(dnsTunnel(towonelv1alpha1.TunnelDNSSpec{}), view, testTargets, tc.optOut)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, ep := range obj.Spec.Endpoints {
				names = append(names, ep.DNSName)
			}
			if !slices.Equal(names, tc.want) {
				t.Errorf("endpoints = %v, want %v", names, tc.want)
			}
			var recNames []string
			for _, r := range records {
				recNames = append(recNames, r.Hostname)
			}
			if !slices.Equal(recNames, tc.want) {
				t.Errorf("records = %v, want %v (same projection as endpoints)", recNames, tc.want)
			}
		})
	}
}

func TestDNSOptOutHostnames(t *testing.T) {
	agent := func(name string, svcs ...towonelv1alpha1.AgentService) towonelv1alpha1.TowonelAgent {
		return towonelv1alpha1.TowonelAgent{
			Name: name, Namespace: "network",
			Spec: towonelv1alpha1.TowonelAgentSpec{Services: svcs},
		}
	}
	svc := func(host string, dnsRecord *bool) towonelv1alpha1.AgentService {
		return towonelv1alpha1.AgentService{Hostname: host, Origin: "o:1", DNSRecord: dnsRecord}
	}
	agents := []towonelv1alpha1.TowonelAgent{
		agent("one", svc("shared.example.com", new(true)), svc("only-one.example.com", nil), svc("private.example.com", new(false))),
		agent("two", svc("shared.example.com", new(false)), svc("explicit-true.example.com", new(true))),
	}
	got := dnsOptOutHostnames(agents)
	want := map[string]bool{"shared.example.com": true, "private.example.com": true}
	if !maps.Equal(got, want) {
		t.Fatalf("opt-out set = %v, want %v (any agent opting out wins; nil and true publish)", got, want)
	}
	if len(dnsOptOutHostnames(nil)) != 0 {
		t.Error("no agents must give an empty set")
	}
}
