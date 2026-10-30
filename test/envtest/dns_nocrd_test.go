package envtest_test

import (
	"path/filepath"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/controller"
)

// TestDNSWithoutDNSEndpointCRD runs a second API server WITHOUT the
// external-dns CRD (design §8): DNSEndpointCRDMissing never blocks Ready,
// Disabled mode sweeps without looping (gate 2 B1), a CRD installed later is
// picked up with no restart (Review Focus 3), and Delete-policy deletion
// completes.
func TestDNSWithoutDNSEndpointCRD(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("second envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	c, _, stop := startManagerOn(t, cfg, testEdgeTargets)
	defer stop()
	ctx := t.Context()
	key := types.NamespacedName{Namespace: "default", Name: "nocrd"}
	tt := dnsTunnelObj(key.Namespace, key.Name, []string{"a.example.com"}, enabled())
	tt.Spec.DeletionPolicy = towonelv1alpha1.DeletionPolicyDelete
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		cond, got := tunnelCond(ctx, c, key.Namespace, key.Name)
		return cond != nil && cond.Reason == controller.ReasonDNSCRDMissing &&
			got != nil && meta.IsStatusConditionTrue(got.Status.Conditions, controller.CondReady)
	})
	mutateTunnel(t, c, key.Namespace, key.Name, func(tt *towonelv1alpha1.TowonelTunnel) { tt.Spec.DNS.Mode = towonelv1alpha1.DNSModeDisabled })
	waitFor(t, 20*time.Second, func() bool {
		cond, got := tunnelCond(ctx, c, key.Namespace, key.Name)
		return got != nil && cond == nil && meta.IsStatusConditionTrue(got.Status.Conditions, controller.CondReady)
	})
	// Delete-policy deletion with the CRD ABSENT: the finalizer's DNS sweep sees
	// NoMatch, treats it as nothing to clean, and must not block deletion.
	delKey := types.NamespacedName{Namespace: "default", Name: "nocrd-del"}
	del := dnsTunnelObj(delKey.Namespace, delKey.Name, []string{"del.example.com"}, enabled())
	del.Spec.DeletionPolicy = towonelv1alpha1.DeletionPolicyDelete
	if err := c.Create(ctx, del); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		cond, _ := tunnelCond(ctx, c, delKey.Namespace, delKey.Name)
		return cond != nil && cond.Reason == controller.ReasonDNSCRDMissing
	})
	if err := c.Delete(ctx, del); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		var g towonelv1alpha1.TowonelTunnel
		return apierrors.IsNotFound(c.Get(ctx, delKey, &g))
	})
	// Late install: re-enable, then install the CRD with no operator restart.
	mutateTunnel(t, c, key.Namespace, key.Name, func(tt *towonelv1alpha1.TowonelTunnel) { tt.Spec.DNS.Mode = towonelv1alpha1.DNSModeDNSEndpoint })
	crd, err := dnsEndpointCRDPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := envtest.InstallCRDs(cfg, envtest.CRDInstallOptions{Paths: []string{crd}, ErrorIfPathMissing: true}); err != nil {
		t.Fatalf("install CRD: %v", err)
	}
	// The 2-minute DNS retry would be too slow for a test: poke a reconcile.
	mutateTunnel(t, c, key.Namespace, key.Name, func(tt *towonelv1alpha1.TowonelTunnel) {
		if tt.Annotations == nil {
			tt.Annotations = map[string]string{}
		}
		tt.Annotations["test.towonel.io/poke"] = "crd-installed"
	})
	waitFor(t, 30*time.Second, func() bool {
		cond, _ := tunnelCond(ctx, c, key.Namespace, key.Name)
		return cond != nil && cond.Reason == controller.ReasonSynced
	})
	if err := c.Delete(ctx, tt); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		var g towonelv1alpha1.TowonelTunnel
		return apierrors.IsNotFound(c.Get(ctx, key, &g))
	})
}
