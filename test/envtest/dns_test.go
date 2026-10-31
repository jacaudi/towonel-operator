package envtest_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	externaldnsv1alpha1 "sigs.k8s.io/external-dns/apis/v1alpha1"
	"sigs.k8s.io/external-dns/endpoint"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/controller"
)

// testEdgeTargets: the fake hub records the tunnel's spec.region (CRD default
// EU) on create, so test invites resolve to EU unless a test sets CA.
var testEdgeTargets = map[string]string{"EU": "edge-eu.example.com", "CA": "edge-na.example.com"}

// testOwnerHash pins the documented LabelOwnerTunnel contract (kubectl
// cleanup selector): first 16 hex chars of sha256("<ns>/<name>").
func testOwnerHash(ns, name string) string {
	sum := sha256.Sum256([]byte(ns + "/" + name))
	return hex.EncodeToString(sum[:])[:16]
}

func dnsName(ns, name string) string { return "towonel." + ns + "." + name }

func dnsTunnelObj(ns, name string, hosts []string, dns towonelv1alpha1.TunnelDNSSpec) *towonelv1alpha1.TowonelTunnel {
	return &towonelv1alpha1.TowonelTunnel{
		Name: name, Namespace: ns,
		Spec: towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: hosts, DNS: dns},
	}
}

func enabled(mut ...func(*towonelv1alpha1.TunnelDNSSpec)) towonelv1alpha1.TunnelDNSSpec {
	d := towonelv1alpha1.TunnelDNSSpec{Mode: towonelv1alpha1.DNSModeDNSEndpoint}
	for _, m := range mut {
		m(&d)
	}
	return d
}

// getDNS reads directly from the API server (k8sClient is uncached).
func getDNS(ctx context.Context, ns, name string) (*externaldnsv1alpha1.DNSEndpoint, error) {
	var o externaldnsv1alpha1.DNSEndpoint
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &o)
	return &o, err
}

func dnsNames(o *externaldnsv1alpha1.DNSEndpoint) []string {
	var out []string
	for _, ep := range o.Spec.Endpoints {
		out = append(out, ep.DNSName)
	}
	return out
}

func tunnelCond(ctx context.Context, c client.Client, ns, name string) (*metav1.Condition, *towonelv1alpha1.TowonelTunnel) {
	var tt towonelv1alpha1.TowonelTunnel
	if c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &tt) != nil {
		return nil, nil
	}
	return meta.FindStatusCondition(tt.Status.Conditions, controller.CondDNSEndpointSynced), &tt
}

func mutateTunnel(t *testing.T, c client.Client, ns, name string, mut func(*towonelv1alpha1.TowonelTunnel)) {
	t.Helper()
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var tt towonelv1alpha1.TowonelTunnel
		if err := c.Get(t.Context(), types.NamespacedName{Namespace: ns, Name: name}, &tt); err != nil {
			return err
		}
		mut(&tt)
		return c.Update(t.Context(), &tt)
	})
	if err != nil {
		t.Fatalf("update tunnel %s/%s: %v", ns, name, err)
	}
}

func TestDNSEndpointPublishedForConfirmedHostnames(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	dns := enabled(func(d *towonelv1alpha1.TunnelDNSSpec) {
		d.DefaultTTL = 120
		d.Labels = map[string]string{"example.com/dns-endpoint": "public"}
		d.Annotations = map[string]string{"note": "x"}
		d.ProviderSpecific = []towonelv1alpha1.DNSProviderProperty{{Name: "cloudflare/proxied", Value: "false"}}
	})
	if err := c.Create(ctx, dnsTunnelObj(ns, "pub", []string{"b.example.com", "a.example.com", "*.example.com"}, dns)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		o, err := getDNS(ctx, ns, dnsName(ns, "pub"))
		return err == nil && slices.Equal(dnsNames(o), []string{"a.example.com", "b.example.com"})
	})
	o, _ := getDNS(ctx, ns, dnsName(ns, "pub"))
	ep := o.Spec.Endpoints[0]
	if ep.RecordType != endpoint.RecordTypeCNAME || !slices.Equal(ep.Targets, endpoint.Targets{"edge-eu.example.com"}) || ep.RecordTTL != 120 ||
		len(ep.ProviderSpecific) != 1 || ep.ProviderSpecific[0].Name != "cloudflare/proxied" || ep.ProviderSpecific[0].Value != "false" {
		t.Fatalf("endpoint = %+v", ep)
	}
	if o.Labels["example.com/dns-endpoint"] != "public" || o.Labels[controller.LabelOwnerTunnel] != testOwnerHash(ns, "pub") ||
		o.Annotations["note"] != "x" || o.Annotations[controller.AnnotationOwnerTunnelRef] != ns+"/pub" || len(o.OwnerReferences) != 0 {
		t.Fatalf("metadata = labels %v annotations %v owners %v", o.Labels, o.Annotations, o.OwnerReferences)
	}
	waitFor(t, 20*time.Second, func() bool {
		cond, tt := tunnelCond(ctx, c, ns, "pub")
		return cond != nil && cond.Status == metav1.ConditionTrue && cond.Reason == controller.ReasonSynced &&
			len(tt.Status.DNSRecords) == 2 && meta.IsStatusConditionTrue(tt.Status.Conditions, controller.CondReady)
	})
}

// Review Focus 1: removed hostnames leave the object; the last one deletes it.
func TestDNSEndpointShrinksThenRemoved(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	if err := c.Create(ctx, dnsTunnelObj(ns, "shrink", []string{"a.example.com", "b.example.com", "*.wild.example.com"}, enabled())); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		o, err := getDNS(ctx, ns, dnsName(ns, "shrink"))
		return err == nil && len(o.Spec.Endpoints) == 2
	})
	mutateTunnel(t, c, ns, "shrink", func(tt *towonelv1alpha1.TowonelTunnel) {
		tt.Spec.ExtraHostnames = []string{"a.example.com", "*.wild.example.com"}
	})
	waitFor(t, 20*time.Second, func() bool {
		o, err := getDNS(ctx, ns, dnsName(ns, "shrink"))
		return err == nil && slices.Equal(dnsNames(o), []string{"a.example.com"})
	})
	mutateTunnel(t, c, ns, "shrink", func(tt *towonelv1alpha1.TowonelTunnel) {
		tt.Spec.ExtraHostnames = []string{"*.wild.example.com"}
	})
	waitFor(t, 20*time.Second, func() bool {
		_, err := getDNS(ctx, ns, dnsName(ns, "shrink"))
		cond, _ := tunnelCond(ctx, c, ns, "shrink")
		return apierrors.IsNotFound(err) && cond != nil && cond.Status == metav1.ConditionTrue &&
			cond.Message == "0 records; DNSEndpoint removed"
	})
}

func TestDNSEndpointMovesWithNamespace(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns, ns2 := mustNamespace(t), mustNamespace(t)
	if err := c.Create(ctx, dnsTunnelObj(ns, "mv", []string{"a.example.com"}, enabled())); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool { _, err := getDNS(ctx, ns, dnsName(ns, "mv")); return err == nil })
	mutateTunnel(t, c, ns, "mv", func(tt *towonelv1alpha1.TowonelTunnel) { tt.Spec.DNS.Namespace = ns2 })
	waitFor(t, 20*time.Second, func() bool {
		_, errNew := getDNS(ctx, ns2, dnsName(ns, "mv"))
		_, errOld := getDNS(ctx, ns, dnsName(ns, "mv"))
		return errNew == nil && apierrors.IsNotFound(errOld)
	})
}

// Review Focus 2: two tunnels sharing one spec.dns.namespace never sweep each other.
func TestDNSTwoTunnelsShareDNSNamespace(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	nsA, nsB, shared := mustNamespace(t), mustNamespace(t), mustNamespace(t)
	in := func(d *towonelv1alpha1.TunnelDNSSpec) { d.Namespace = shared }
	if err := c.Create(ctx, dnsTunnelObj(nsA, "x", []string{"a.example.com"}, enabled(in))); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, dnsTunnelObj(nsB, "x", []string{"b.example.com"}, enabled(in))); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		_, errA := getDNS(ctx, shared, dnsName(nsA, "x"))
		_, errB := getDNS(ctx, shared, dnsName(nsB, "x"))
		return errA == nil && errB == nil
	})
	mutateTunnel(t, c, nsA, "x", func(tt *towonelv1alpha1.TowonelTunnel) { tt.Spec.DNS.Mode = towonelv1alpha1.DNSModeDisabled })
	waitFor(t, 20*time.Second, func() bool { _, err := getDNS(ctx, shared, dnsName(nsA, "x")); return apierrors.IsNotFound(err) })
	if _, err := getDNS(ctx, shared, dnsName(nsB, "x")); err != nil {
		t.Fatalf("disabling tunnel A swept tunnel B's DNSEndpoint: %v", err)
	}
}

func TestDNSDisabledSweepsAndClearsCondition(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	if err := c.Create(ctx, dnsTunnelObj(ns, "off", []string{"a.example.com"}, enabled())); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool { _, err := getDNS(ctx, ns, dnsName(ns, "off")); return err == nil })
	mutateTunnel(t, c, ns, "off", func(tt *towonelv1alpha1.TowonelTunnel) { tt.Spec.DNS.Mode = towonelv1alpha1.DNSModeDisabled })
	waitFor(t, 20*time.Second, func() bool {
		_, err := getDNS(ctx, ns, dnsName(ns, "off"))
		cond, tt := tunnelCond(ctx, c, ns, "off")
		// Disabled: object swept, condition REMOVED, intent still shown in dnsRecords.
		return apierrors.IsNotFound(err) && tt != nil && cond == nil && len(tt.Status.DNSRecords) == 1
	})
}

func TestDNSRevokedInviteWithdrawsRecords(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, hub, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	if err := c.Create(ctx, dnsTunnelObj(ns, "rev", []string{"a.example.com"}, enabled())); err != nil {
		t.Fatal(err)
	}
	var inviteID string
	waitFor(t, 20*time.Second, func() bool {
		_, err := getDNS(ctx, ns, dnsName(ns, "rev"))
		_, tt := tunnelCond(ctx, c, ns, "rev")
		if tt != nil {
			inviteID = tt.Status.InviteID
		}
		return err == nil && inviteID != ""
	})
	hub.Revoke(inviteID)
	pokeTunnel(t, c, ns, "rev")
	waitFor(t, 20*time.Second, func() bool {
		_, err := getDNS(ctx, ns, dnsName(ns, "rev"))
		cond, tt := tunnelCond(ctx, c, ns, "rev")
		if tt == nil {
			return false
		}
		// A revoked invite is not "converged": HostnamesSynced must say so.
		hostCond := meta.FindStatusCondition(tt.Status.Conditions, controller.CondHostnamesSynced)
		return apierrors.IsNotFound(err) && cond != nil && cond.Reason == controller.ReasonInviteRevoked &&
			len(tt.Status.DNSRecords) == 0 &&
			hostCond != nil && hostCond.Status == metav1.ConditionFalse && hostCond.Reason == controller.ReasonInviteRevoked
	})
}

// A hub-deleted invite (GET /v1/invites/{id} -> 404) cannot be confirmed, so
// DNS fails closed: the records are withdrawn rather than left published.
func TestDNSInviteDeletedHubSideWithdrawsRecords(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, hub, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	if err := c.Create(ctx, dnsTunnelObj(ns, "gone", []string{"a.example.com"}, enabled())); err != nil {
		t.Fatal(err)
	}
	var inviteID string
	waitFor(t, 20*time.Second, func() bool {
		_, err := getDNS(ctx, ns, dnsName(ns, "gone"))
		_, tt := tunnelCond(ctx, c, ns, "gone")
		if tt != nil {
			inviteID = tt.Status.InviteID
		}
		return err == nil && inviteID != ""
	})
	srv, tc := hub.Server()
	defer srv.Close()
	if err := tc.DeleteInvite(ctx, inviteID); err != nil {
		t.Fatalf("delete invite hub-side: %v", err)
	}
	pokeTunnel(t, c, ns, "gone")
	waitFor(t, 20*time.Second, func() bool {
		_, err := getDNS(ctx, ns, dnsName(ns, "gone"))
		cond, tt := tunnelCond(ctx, c, ns, "gone")
		if tt == nil {
			return false
		}
		// r.fail ran (Ready=False/APIError) and DNS recorded why it withdrew.
		ready := meta.FindStatusCondition(tt.Status.Conditions, controller.CondReady)
		return apierrors.IsNotFound(err) && cond != nil && cond.Reason == controller.ReasonInviteRevoked &&
			ready != nil && ready.Status == metav1.ConditionFalse && ready.Reason == controller.ReasonAPIError
	})
}

// A hostname DELETE that 404s ("hostname not found on invite": another writer
// already removed it, a multi-cluster race) is not a deleted invite. The
// reconcile fails, but the published records must stay: only a 404 on
// GET /v1/invites/{id} withdraws them.
func TestDNSHostnameRemove404KeepsRecords(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, hub, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	if err := c.Create(ctx, dnsTunnelObj(ns, "race", []string{"a.example.com", "b.example.com"}, enabled())); err != nil {
		t.Fatal(err)
	}
	var inviteID string
	waitFor(t, 20*time.Second, func() bool {
		o, err := getDNS(ctx, ns, dnsName(ns, "race"))
		_, tt := tunnelCond(ctx, c, ns, "race")
		if tt != nil {
			inviteID = tt.Status.InviteID
		}
		return err == nil && inviteID != "" && slices.Equal(dnsNames(o), []string{"a.example.com", "b.example.com"})
	})
	hub.SetHostnameDeleteNotFound(inviteID, "b.example.com", true)
	mutateTunnel(t, c, ns, "race", func(tt *towonelv1alpha1.TowonelTunnel) {
		tt.Spec.ExtraHostnames = []string{"a.example.com"} // drops b -> hub DELETE 404s
	})
	// The remove failure surfaces on HostnamesSynced; DNS runs after it in the
	// same pass, so by now a wrongful withdraw has already happened.
	waitFor(t, 20*time.Second, func() bool {
		_, tt := tunnelCond(ctx, c, ns, "race")
		if tt == nil {
			return false
		}
		hostCond := meta.FindStatusCondition(tt.Status.Conditions, controller.CondHostnamesSynced)
		return hostCond != nil && hostCond.Status == metav1.ConditionFalse && hostCond.Reason == controller.ReasonAPIError &&
			strings.Contains(hostCond.Message, "remove hostname")
	})
	o, err := getDNS(ctx, ns, dnsName(ns, "race"))
	if err != nil {
		t.Fatalf("DNSEndpoint withdrawn after a hostname-remove 404: %v", err)
	}
	if got := dnsNames(o); !slices.Equal(got, []string{"a.example.com", "b.example.com"}) {
		t.Fatalf("DNSEndpoint names = %v, want both kept", got)
	}
	cond, _ := tunnelCond(ctx, c, ns, "race")
	if cond != nil && cond.Reason == controller.ReasonInviteRevoked {
		t.Fatalf("DNSEndpointSynced = %+v, must not be InviteRevoked", cond)
	}
}

// pokeTunnel annotates the tunnel with a fresh value to trigger a reconcile.
func pokeTunnel(t *testing.T, c client.Client, ns, name string) {
	t.Helper()
	mutateTunnel(t, c, ns, name, func(tt *towonelv1alpha1.TowonelTunnel) {
		if tt.Annotations == nil {
			tt.Annotations = map[string]string{}
		}
		tt.Annotations["test.towonel.io/poke"] = fmt.Sprint(time.Now().UnixNano())
	})
}

func TestDNSNoEdgeTargetSweeps(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, map[string]string{}) // a self-hosted hub with no map
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	dns := enabled(func(d *towonelv1alpha1.TunnelDNSSpec) { d.CNAMETarget = "custom.example.com" })
	if err := c.Create(ctx, dnsTunnelObj(ns, "nt", []string{"a.example.com"}, dns)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool { _, err := getDNS(ctx, ns, dnsName(ns, "nt")); return err == nil })
	mutateTunnel(t, c, ns, "nt", func(tt *towonelv1alpha1.TowonelTunnel) { tt.Spec.DNS.CNAMETarget = "" })
	waitFor(t, 20*time.Second, func() bool {
		_, err := getDNS(ctx, ns, dnsName(ns, "nt"))
		cond, _ := tunnelCond(ctx, c, ns, "nt")
		return apierrors.IsNotFound(err) && cond != nil && cond.Status == metav1.ConditionFalse && cond.Reason == controller.ReasonNoEdgeTarget
	})
}

func TestDNSForeignObjectNotOverwritten(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns, ns2 := mustNamespace(t), mustNamespace(t)
	foreign := &externaldnsv1alpha1.DNSEndpoint{
		Name: dnsName(ns, "fo"), Namespace: ns,
		Spec: externaldnsv1alpha1.DNSEndpointSpec{Endpoints: []*endpoint.Endpoint{{
			DNSName: "keep.example.com", RecordType: endpoint.RecordTypeA, Targets: endpoint.Targets{"192.0.2.1"},
		}}},
	}
	if err := k8sClient.Create(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	stale := &externaldnsv1alpha1.DNSEndpoint{Name: "stale", Namespace: ns2, Labels: map[string]string{
		"app.kubernetes.io/managed-by": "towonel-operator", controller.LabelOwnerTunnel: testOwnerHash(ns, "fo"),
	}}
	if err := k8sClient.Create(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, dnsTunnelObj(ns, "fo", []string{"a.example.com"}, enabled())); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		cond, _ := tunnelCond(ctx, c, ns, "fo")
		_, errStale := getDNS(ctx, ns2, "stale")
		return cond != nil && cond.Reason == controller.ReasonForeignObject && apierrors.IsNotFound(errStale)
	})
	o, err := getDNS(ctx, ns, dnsName(ns, "fo"))
	if err != nil || !slices.Equal(dnsNames(o), []string{"keep.example.com"}) {
		t.Fatalf("foreign object overwritten: %v %v", err, o)
	}
	// Events are recorded asynchronously by the broadcaster: wait, don't read once.
	waitFor(t, 20*time.Second, func() bool {
		var evs corev1.EventList
		if k8sClient.List(ctx, &evs, client.InNamespace(ns)) != nil {
			return false
		}
		return slices.ContainsFunc(evs.Items, func(e corev1.Event) bool { return e.Reason == controller.ReasonForeignObject })
	})
}

func TestDNSDeletePolicyWithdrawsRecords(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	tt := dnsTunnelObj(ns, "del", []string{"a.example.com"}, enabled())
	tt.Spec.DeletionPolicy = towonelv1alpha1.DeletionPolicyDelete
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool { _, err := getDNS(ctx, ns, dnsName(ns, "del")); return err == nil })
	if err := c.Delete(ctx, tt); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		var g towonelv1alpha1.TowonelTunnel
		_, err := getDNS(ctx, ns, dnsName(ns, "del"))
		return apierrors.IsNotFound(err) && apierrors.IsNotFound(c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "del"}, &g))
	})
}

// Design §6.5 / gate 2 B4: the Delete-policy sweep needs no hub access, so it
// runs before resolveAPIKey — DNS is withdrawn even while the finalizer is
// stuck on an unresolvable API key.
func TestDNSDeleteSweepsWithoutAPIKey(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	tt := dnsTunnelObj(ns, "nokey", []string{"a.example.com"}, enabled())
	tt.Spec.DeletionPolicy = towonelv1alpha1.DeletionPolicyDelete
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool { _, err := getDNS(ctx, ns, dnsName(ns, "nokey")); return err == nil })
	// The DNSEndpoint is applied before status is written; mutating the spec in
	// that window conflicts the status write, losing inviteId, and an invite-less
	// delete never needs the API key. Wait for inviteId to persist first.
	waitFor(t, 20*time.Second, func() bool {
		var g towonelv1alpha1.TowonelTunnel
		return c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "nokey"}, &g) == nil && g.Status.InviteID != ""
	})
	mutateTunnel(t, c, ns, "nokey", func(tt *towonelv1alpha1.TowonelTunnel) {
		tt.Spec.APIKeySecretRef = &towonelv1alpha1.SecretKeyRef{Name: "missing-secret"}
	})
	if err := c.Delete(ctx, tt); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool { _, err := getDNS(ctx, ns, dnsName(ns, "nokey")); return apierrors.IsNotFound(err) })
	// Unblock the finalizer so the namespace is not left with a stuck object.
	mutateTunnel(t, c, ns, "nokey", func(tt *towonelv1alpha1.TowonelTunnel) { tt.Spec.APIKeySecretRef = nil })
	waitFor(t, 20*time.Second, func() bool {
		var g towonelv1alpha1.TowonelTunnel
		return apierrors.IsNotFound(c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "nokey"}, &g))
	})
}

// retainDeleted creates a Retain-policy DNS tunnel, waits for its record, deletes
// the tunnel, and asserts the DNSEndpoint survives the deletion.
func retainDeleted(t *testing.T, c client.Client, ns, name string) func() *towonelv1alpha1.TowonelTunnel {
	t.Helper()
	ctx := t.Context()
	mk := func() *towonelv1alpha1.TowonelTunnel {
		tt := dnsTunnelObj(ns, name, []string{"a.example.com"}, enabled())
		tt.Spec.DeletionPolicy = towonelv1alpha1.DeletionPolicyRetain
		return tt
	}
	tt := mk()
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool { _, err := getDNS(ctx, ns, dnsName(ns, name)); return err == nil })
	if err := c.Delete(ctx, tt); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		var g towonelv1alpha1.TowonelTunnel
		return apierrors.IsNotFound(c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &g))
	})
	if _, err := getDNS(ctx, ns, dnsName(ns, name)); err != nil {
		t.Fatalf("Retain: DNSEndpoint must survive tunnel deletion, got %v", err)
	}
	return mk
}

// Token-PRESENT path only. envtest has no garbage collector, so the token
// Secret (controller-owned by the deleted tunnel, towoneltunnel_secret.go)
// survives here. In a real cluster GC deletes it, and the recreated tunnel
// stops at errAdoptedNoToken before DNS runs; that path is the next test.
func TestDNSRetainKeepsRecordsAndReadoptsWithToken(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	mk := retainDeleted(t, c, ns, "keep")
	if err := c.Create(ctx, mk()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		cond, _ := tunnelCond(ctx, c, ns, "keep")
		return cond != nil && cond.Status == metav1.ConditionTrue && cond.Reason == controller.ReasonSynced
	})
}

// Real-cluster Retain path (S4): with the token Secret gone (what GC does), the
// recreated tunnel adopts the invite but halts at errAdoptedNoToken
// (Ready=False/Reconciling, Phase Pending) before DNS runs. The records stay
// published but UNMANAGED, and the tunnel never reports DNSEndpointSynced.
func TestDNSRetainWithoutTokenLeavesRecordsUnmanaged(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	mk := retainDeleted(t, c, ns, "keepnotoken")
	if err := k8sClient.Delete(ctx, &corev1.Secret{Name: "keepnotoken-token", Namespace: ns}); err != nil {
		t.Fatalf("delete token Secret: %v", err)
	}
	// c is the manager's CACHED client: wait until the cache has seen the delete,
	// or the recreated tunnel could read a stale Secret and skip the no-token path.
	waitFor(t, 20*time.Second, func() bool {
		var s corev1.Secret
		return apierrors.IsNotFound(c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "keepnotoken-token"}, &s))
	})
	if err := c.Create(ctx, mk()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		cond, got := tunnelCond(ctx, c, ns, "keepnotoken")
		if got == nil {
			return false
		}
		ready := meta.FindStatusCondition(got.Status.Conditions, controller.CondReady)
		// InviteID != "": the invite was ADOPTED (no token), not "not yet issued".
		return cond == nil && got.Status.InviteID != "" && got.Status.Phase == "Pending" && ready != nil &&
			ready.Status == metav1.ConditionFalse && ready.Reason == controller.ReasonReconciling
	})
	if _, err := getDNS(ctx, ns, dnsName(ns, "keepnotoken")); err != nil {
		t.Fatalf("records must remain (unmanaged) when the token is missing, got %v", err)
	}
}

// dnsCondEventually waits for the tunnel's DNSEndpointSynced condition to be
// False with the given reason and returns it.
func dnsCondEventually(t *testing.T, c client.Client, ns, name, reason string) *metav1.Condition {
	t.Helper()
	var got *metav1.Condition
	waitFor(t, 20*time.Second, func() bool {
		cond, _ := tunnelCond(t.Context(), c, ns, name)
		got = cond
		return cond != nil && cond.Status == metav1.ConditionFalse && cond.Reason == reason
	})
	return got
}

// A label key the API server rejects (the CRD only bounds the map shape) must
// surface as InvalidConfig, not a retry loop.
func TestDNSInvalidLabelsReportInvalidConfig(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ns := mustNamespace(t)
	dns := enabled(func(d *towonelv1alpha1.TunnelDNSSpec) { d.Labels = map[string]string{"bad key!": "x"} })
	if err := c.Create(t.Context(), dnsTunnelObj(ns, "badlabels", []string{"a.example.com"}, dns)); err != nil {
		t.Fatal(err)
	}
	dnsCondEventually(t, c, ns, "badlabels", controller.ReasonInvalidConfig)
	if _, err := getDNS(t.Context(), ns, dnsName(ns, "badlabels")); !apierrors.IsNotFound(err) {
		t.Fatalf("DNSEndpoint must not exist for invalid labels, got err=%v", err)
	}
}

// spec.dns.namespace naming a namespace that does not exist is an APIError
// that says why (the operator retries; creating the namespace fixes it).
func TestDNSMissingNamespaceReportsAPIError(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ns := mustNamespace(t)
	dns := enabled(func(d *towonelv1alpha1.TunnelDNSSpec) { d.Namespace = ns + "-missing" })
	if err := c.Create(t.Context(), dnsTunnelObj(ns, "nonamespace", []string{"a.example.com"}, dns)); err != nil {
		t.Fatal(err)
	}
	cond := dnsCondEventually(t, c, ns, "nonamespace", controller.ReasonAPIError)
	if !strings.Contains(cond.Message, "not found") {
		t.Fatalf("message = %q, want it to contain %q", cond.Message, "not found")
	}
}

const optOutPrivate, optOutPublic, optOutExtra = "private.example.com", "public.example.com", "extra.example.com"

// optOutFixture creates a DNSEndpoint-mode tunnel "dnstun" in ns with two HTTPRoutes
// under one Gateway: "private" annotated towonel.io/dns-record: "false", and an
// un-annotated sibling "public"; spec.extraHostnames carries optOutExtra. It waits
// until both route hostnames are hub-authorized and the sibling is published.
func optOutFixture(t *testing.T, c client.Client, ns string) {
	t.Helper()
	ctx := t.Context()
	if err := c.Create(ctx, annService(ns, "envoy", nil, corev1.ServicePort{Port: 443})); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, autoRoutesGateway(ns, "gw", "envoy")); err != nil {
		t.Fatal(err)
	}
	routeAnn := func(extra map[string]string) map[string]string {
		ann := map[string]string{controller.AnnotationTunnel: "true", controller.AnnotationTunnelRef: "dnstun"}
		maps.Copy(ann, extra)
		return ann
	}
	for _, rt := range []*gwv1.HTTPRoute{
		httpRoute(ns, "private", routeAnn(map[string]string{controller.AnnotationDNSRecord: "false"}), "gw", optOutPrivate),
		httpRoute(ns, "public", routeAnn(nil), "gw", optOutPublic),
	} {
		if err := c.Create(ctx, rt); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Create(ctx, dnsTunnelObj(ns, "dnstun", []string{optOutExtra}, enabled())); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 60*time.Second, func() bool {
		_, tt := tunnelCond(ctx, c, ns, "dnstun")
		o, err := getDNS(ctx, ns, dnsName(ns, "dnstun"))
		return tt != nil && slices.Contains(tt.Status.AuthorizedHostnames, optOutPrivate) &&
			slices.Contains(tt.Status.AuthorizedHostnames, optOutPublic) &&
			err == nil && slices.Contains(dnsNames(o), optOutPublic)
	})
}

// A route annotated towonel.io/dns-record: "false" stays authorized at the hub
// (it lands in status.authorizedHostnames) but is kept out of the DNSEndpoint and
// status.dnsRecords; its un-annotated sibling and spec.extraHostnames publish.
func TestDNSRecordAnnotationKeepsHostnameOutOfDNSEndpoint(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	optOutFixture(t, c, ns)

	// The opted-out name never appears, across a stability window.
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		o, err := getDNS(ctx, ns, dnsName(ns, "dnstun"))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := dnsNames(o), []string{optOutExtra, optOutPublic}; !slices.Equal(got, want) {
			t.Fatalf("endpoints = %v, want %v (opted-out %s excluded)", got, want, optOutPrivate)
		}
	}
	_, tt := tunnelCond(ctx, c, ns, "dnstun")
	for _, r := range tt.Status.DNSRecords {
		if r.Hostname == optOutPrivate {
			t.Fatalf("status.dnsRecords lists opted-out %s: %+v", optOutPrivate, tt.Status.DNSRecords)
		}
	}
	if len(tt.Status.DNSRecords) != 2 {
		t.Fatalf("status.dnsRecords = %+v, want the %s and %s records", tt.Status.DNSRecords, optOutExtra, optOutPublic)
	}
}

// Removing the annotation releases the source's dnsRecord:false (server-side apply
// drops the field it owned), so the agent field returns to nil and the hostname is
// republished.
func TestDNSRecordAnnotationRemovalRepublishesHostname(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManagerOn(t, sharedCfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	ns := mustNamespace(t)
	optOutFixture(t, c, ns)

	agentNN := defaultAgentNN(ns, types.NamespacedName{Namespace: ns, Name: "dnstun"})
	agentDNSRecord := func() (found bool, v *bool) {
		var ta towonelv1alpha1.TowonelAgent
		if k8sClient.Get(ctx, agentNN, &ta) != nil {
			return false, nil
		}
		for _, s := range ta.Spec.Services {
			if s.Hostname == optOutPrivate {
				return true, s.DNSRecord
			}
		}
		return false, nil
	}
	if found, v := agentDNSRecord(); !found || v == nil || *v {
		t.Fatalf("before removal: agent service %s found=%t dnsRecord=%v, want dnsRecord=false", optOutPrivate, found, v)
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var rt gwv1.HTTPRoute
		if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "private"}, &rt); err != nil {
			return err
		}
		delete(rt.Annotations, controller.AnnotationDNSRecord)
		return c.Update(ctx, &rt)
	})
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, 60*time.Second, func() bool {
		found, v := agentDNSRecord()
		o, derr := getDNS(ctx, ns, dnsName(ns, "dnstun"))
		return found && v == nil && derr == nil &&
			slices.Equal(dnsNames(o), []string{optOutExtra, optOutPrivate, optOutPublic})
	})
}
