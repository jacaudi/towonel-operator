package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
// labels/annotations win over user ones; no owner reference is set. optOut names
// hostnames kept out of the endpoints and records (see dnsOptOutHostnames).
func desiredDNSEndpoint(tt *towonelv1alpha1.TowonelTunnel, view inviteView, edgeTargets map[string]string, optOut map[string]bool) (*externaldnsv1alpha1.DNSEndpoint, []towonelv1alpha1.DNSRecord, error) {
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
			if (strings.HasPrefix(h, "*.") && !dns.IncludeWildcards) || optOut[h] {
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

//+kubebuilder:rbac:groups=externaldns.k8s.io,resources=dnsendpoints,verbs=get;list;create;patch;delete

// setDNSCond sets DNSEndpointSynced and records a Warning only when it ENTERS
// False or changes reason — reason-gated (unlike ports' message gate) so
// per-hostname message churn never spams events.
func (r *TowonelTunnelReconciler) setDNSCond(tt *towonelv1alpha1.TowonelTunnel, status metav1.ConditionStatus, reason, msg string) {
	prev := meta.FindStatusCondition(tt.Status.Conditions, CondDNSEndpointSynced)
	if status == metav1.ConditionFalse && r.Recorder != nil &&
		(prev == nil || prev.Status != metav1.ConditionFalse || prev.Reason != reason) {
		r.Recorder.Event(tt, corev1.EventTypeWarning, reason, msg)
	}
	setCond(tt, CondDNSEndpointSynced, status, reason, msg)
}

// ownedByTunnel reports whether obj carries this operator's ownership labels for hash.
func ownedByTunnel(obj *externaldnsv1alpha1.DNSEndpoint, hash string) bool {
	return obj.Labels[LabelManagedBy] == ManagedByValue && obj.Labels[LabelOwnerTunnel] == hash
}

// sweepDNSEndpoints deletes every DNSEndpoint this tunnel owns (by hash, in all
// namespaces) except keep. A NoMatch (CRD not installed) means nothing can
// exist and returns nil — every caller inherits that (design §6.5, gate 2 B1).
func (r *TowonelTunnelReconciler) sweepDNSEndpoints(ctx context.Context, hash string, keep *types.NamespacedName) error {
	var list externaldnsv1alpha1.DNSEndpointList
	if err := r.reader().List(ctx, &list, client.MatchingLabels{LabelManagedBy: ManagedByValue, LabelOwnerTunnel: hash}); err != nil {
		if meta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("list DNSEndpoints: %w", err)
	}
	var errs []error
	for i := range list.Items {
		item := &list.Items[i]
		if keep != nil && item.Namespace == keep.Namespace && item.Name == keep.Name {
			continue
		}
		if err := r.Delete(ctx, item); client.IgnoreNotFound(err) != nil {
			errs = append(errs, fmt.Errorf("delete DNSEndpoint %s/%s: %w", item.Namespace, item.Name, err))
		}
	}
	return errors.Join(errs...)
}

// reconcileDNS converges the tunnel's external-dns DNSEndpoint (design §6.4).
// It never fails the reconcile: problems surface on DNSEndpointSynced, and a
// true return asks Reconcile for dnsRetryRequeue instead of the 30-minute
// resync. The sweep is unconditional — no "evidence" gating (gate 2).
func (r *TowonelTunnelReconciler) reconcileDNS(ctx context.Context, tt *towonelv1alpha1.TowonelTunnel, view inviteView, optOut map[string]bool) (retry bool) {
	desired, records, derr := desiredDNSEndpoint(tt, view, r.DNSEdgeTargets, optOut)
	if derr != nil || view.Revoked {
		tt.Status.DNSRecords = nil
	} else {
		tt.Status.DNSRecords = records // intent, in both modes
	}
	hash := ownerHash(tt.Namespace, tt.Name)
	withdraw := func() bool {
		if err := r.sweepDNSEndpoints(ctx, hash, nil); err != nil {
			r.setDNSCond(tt, metav1.ConditionFalse, ReasonAPIError, err.Error())
			return false
		}
		return true
	}

	switch {
	case tt.Spec.DNS.Mode != towonelv1alpha1.DNSModeDNSEndpoint: // Disabled, or "" when spec.dns is absent
		if !withdraw() {
			return true
		}
		meta.RemoveStatusCondition(&tt.Status.Conditions, CondDNSEndpointSynced)
		return false
	case view.Revoked:
		if !withdraw() {
			return true
		}
		r.setDNSCond(tt, metav1.ConditionFalse, ReasonInviteRevoked,
			fmt.Sprintf("hub reports invite %s revoked; DNS records withdrawn", tt.Status.InviteID))
		return false
	case errors.Is(derr, errNoEdgeTarget):
		if !withdraw() {
			return true
		}
		r.setDNSCond(tt, metav1.ConditionFalse, ReasonNoEdgeTarget,
			fmt.Sprintf("no DNS edge target for region %s: set spec.dns.cnameTarget or --dns-edge-target %s=<fqdn>", view.Region, view.Region))
		return false
	case len(desired.Spec.Endpoints) == 0:
		// Typed SSA from N endpoints to 0 is rejected ("spec null", gate 1 B2): delete instead.
		if !withdraw() {
			return true
		}
		r.setDNSCond(tt, metav1.ConditionTrue, ReasonSynced, "0 records; DNSEndpoint removed")
		return false
	}

	key := types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}
	// Sweep first, so a squatter at our name never blocks cleanup elsewhere.
	if err := r.sweepDNSEndpoints(ctx, hash, &key); err != nil {
		r.setDNSCond(tt, metav1.ConditionFalse, ReasonAPIError, err.Error())
		return true
	}
	var existing externaldnsv1alpha1.DNSEndpoint
	switch err := r.reader().Get(ctx, key, &existing); {
	case err == nil:
		if !ownedByTunnel(&existing, hash) {
			r.setDNSCond(tt, metav1.ConditionFalse, ReasonForeignObject,
				fmt.Sprintf("DNSEndpoint %s exists and is not managed by this tunnel; not overwriting", key))
			return true
		}
	case meta.IsNoMatchError(err):
		r.setDNSCond(tt, metav1.ConditionFalse, ReasonDNSCRDMissing,
			"external-dns DNSEndpoint CRD (externaldns.k8s.io/v1alpha1) is not installed")
		return true
	case !apierrors.IsNotFound(err):
		r.setDNSCond(tt, metav1.ConditionFalse, ReasonAPIError, fmt.Sprintf("get DNSEndpoint %s: %v", key, err))
		return true
	}
	if err := r.Patch(ctx, desired, client.Apply, client.FieldOwner(FieldOwner), client.ForceOwnership); err != nil {
		switch {
		case apierrors.IsInvalid(err):
			r.setDNSCond(tt, metav1.ConditionFalse, ReasonInvalidConfig, fmt.Sprintf("apply DNSEndpoint %s: %v", key, err))
			return false
		case apierrors.IsNotFound(err):
			r.setDNSCond(tt, metav1.ConditionFalse, ReasonAPIError, fmt.Sprintf("namespace %s not found", key.Namespace))
			return true
		default:
			r.setDNSCond(tt, metav1.ConditionFalse, ReasonAPIError, fmt.Sprintf("apply DNSEndpoint %s: %v", key, err))
			return true
		}
	}
	r.setDNSCond(tt, metav1.ConditionTrue, ReasonSynced, fmt.Sprintf("%d record(s) in %s", len(desired.Spec.Endpoints), key))
	return false
}

// dnsOptOutHostnames collects the hostnames kept out of the DNS handoff: any
// referencing agent service with dnsRecord:false excludes that hostname, even
// if another agent serves it without the opt-out (explicit opt-out wins; fail
// closed). spec.extraHostnames have no opt-out.
func dnsOptOutHostnames(agents []towonelv1alpha1.TowonelAgent) map[string]bool {
	out := map[string]bool{}
	for i := range agents {
		for _, svc := range agents[i].Spec.Services {
			if svc.DNSRecord != nil && !*svc.DNSRecord {
				out[svc.Hostname] = true
			}
		}
	}
	return out
}
