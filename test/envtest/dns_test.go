package envtest_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
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
	mutateTunnel(t, c, ns, "rev", func(tt *towonelv1alpha1.TowonelTunnel) {
		if tt.Annotations == nil {
			tt.Annotations = map[string]string{}
		}
		tt.Annotations["test.towonel.io/poke"] = fmt.Sprint(time.Now().UnixNano()) // trigger a reconcile
	})
	waitFor(t, 20*time.Second, func() bool {
		_, err := getDNS(ctx, ns, dnsName(ns, "rev"))
		cond, tt := tunnelCond(ctx, c, ns, "rev")
		return apierrors.IsNotFound(err) && cond != nil && cond.Reason == controller.ReasonInviteRevoked &&
			tt != nil && len(tt.Status.DNSRecords) == 0
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
		return cond == nil && got.Status.Phase == "Pending" && ready != nil &&
			ready.Status == metav1.ConditionFalse && ready.Reason == controller.ReasonReconciling
	})
	if _, err := getDNS(ctx, ns, dnsName(ns, "keepnotoken")); err != nil {
		t.Fatalf("records must remain (unmanaged) when the token is missing, got %v", err)
	}
}
