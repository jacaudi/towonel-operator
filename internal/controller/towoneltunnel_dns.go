package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"strings"

	externaldnsv1alpha1 "sigs.k8s.io/external-dns/apis/v1alpha1"
	"sigs.k8s.io/external-dns/endpoint"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// errNoEdgeTarget: neither spec.dns.cnameTarget nor the operator's edge-target
// map names a CNAME target for the invite's hub region (design §6.3).
var errNoEdgeTarget = errors.New("no DNS edge target for the invite's hub region")

// ownerHash is the stable per-tunnel identity stamped on emitted DNSEndpoints
// (LabelOwnerTunnel): the first 16 hex chars of sha256("<namespace>/<name>").
// Stable across tunnel recreation, so a Retained object is re-adopted.
func ownerHash(namespace, name string) string {
	sum := sha256.Sum256([]byte(namespace + "/" + name))
	return hex.EncodeToString(sum[:])[:16]
}

// dnsEndpointName is towonel.<namespace>.<name> — injective, because
// namespaces contain no dots — or towonel.<ownerHash> when that exceeds the
// 253-character object-name limit.
func dnsEndpointName(namespace, name string) string {
	if n := "towonel." + namespace + "." + name; len(n) <= 253 {
		return n
	}
	return "towonel." + ownerHash(namespace, name)
}

// desiredDNSEndpoint is the single source of the tunnel's DNS intent (design
// §6.3): the DNSEndpoint to apply and its status.dnsRecords projection. Pure.
// The target is spec.dns.cnameTarget, else edgeTargets[view.Region] — the HUB
// region, never spec.region. A revoked invite yields zero endpoints. Operator
// labels/annotations win over user ones; no owner reference is set.
func desiredDNSEndpoint(tt *towonelv1alpha1.TowonelTunnel, view inviteView, edgeTargets map[string]string) (*externaldnsv1alpha1.DNSEndpoint, []towonelv1alpha1.DNSRecord, error) {
	dns := tt.Spec.DNS
	target := strings.TrimSuffix(dns.CNAMETarget, ".")
	if target == "" {
		target = edgeTargets[view.Region]
	}
	if target == "" {
		return nil, nil, errNoEdgeTarget
	}
	ns := dns.Namespace
	if ns == "" {
		ns = tt.Namespace
	}
	labels := maps.Clone(dns.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels[LabelManagedBy] = ManagedByValue
	labels[LabelOwnerTunnel] = ownerHash(tt.Namespace, tt.Name)
	annotations := maps.Clone(dns.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[AnnotationOwnerTunnelRef] = tt.Namespace + "/" + tt.Name

	var eps []*endpoint.Endpoint
	var records []towonelv1alpha1.DNSRecord
	if !view.Revoked {
		for _, h := range dedupe(view.Confirmed) {
			if strings.HasPrefix(h, "*.") && !dns.IncludeWildcards {
				continue
			}
			eps = append(eps, &endpoint.Endpoint{
				DNSName:          h,
				RecordType:       endpoint.RecordTypeCNAME,
				Targets:          endpoint.Targets{target},
				RecordTTL:        endpoint.TTL(dns.DefaultTTL),
				ProviderSpecific: toProviderSpecific(dns.ProviderSpecific),
			})
			records = append(records, towonelv1alpha1.DNSRecord{Hostname: h, Type: endpoint.RecordTypeCNAME, Target: target})
		}
	}
	return &externaldnsv1alpha1.DNSEndpoint{
		APIVersion: externaldnsv1alpha1.GroupVersion.String(), Kind: "DNSEndpoint",
		Name: dnsEndpointName(tt.Namespace, tt.Name), Namespace: ns,
		Labels: labels, Annotations: annotations,
		Spec: externaldnsv1alpha1.DNSEndpointSpec{Endpoints: eps},
	}, records, nil
}

// toProviderSpecific maps spec.dns.providerSpecific to a FRESH external-dns
// slice (one per endpoint, so endpoints never share backing arrays).
func toProviderSpecific(in []towonelv1alpha1.DNSProviderProperty) endpoint.ProviderSpecific {
	if len(in) == 0 {
		return nil
	}
	out := make(endpoint.ProviderSpecific, 0, len(in))
	for _, p := range in {
		out = append(out, endpoint.ProviderSpecificProperty{Name: p.Name, Value: p.Value})
	}
	return out
}
