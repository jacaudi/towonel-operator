package envtest_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/controller"
	"github.com/jacaudi/towonel-operator/internal/towonel/towoneltest"
)

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

// startManager wires the reconciler to envtest + a fresh fake hub.
func startManager(t *testing.T) (client.Client, *towoneltest.Hub, func()) {
	t.Helper()
	hub := towoneltest.NewHub()
	srv, _ := hub.Server()
	mgr, err := ctrl.NewManager(sharedCfg, managerOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.RegisterIndexes(context.Background(), mgr); err != nil {
		t.Fatal(err)
	}
	r := &controller.TowonelTunnelReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Recorder:   mgr.GetEventRecorderFor("towoneltunnel-" + t.Name()),
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}
	if err := r.SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	ar := &controller.TowonelAgentReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("towonelagent-" + t.Name()),
	}
	if err := ar.SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = mgr.Start(ctx) }()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache sync failed")
	}
	return mgr.GetClient(), hub, func() { cancel(); srv.Close() }
}

func TestReconcileCreatesInviteAndSecret(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()
	tt := &towonelv1alpha1.TowonelTunnel{
		ObjectMeta: metav1.ObjectMeta{Name: "app8", Namespace: "default"},
		Spec:       towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"a.example"}},
	}
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, func() bool {
		var got towonelv1alpha1.TowonelTunnel
		if err := c.Get(ctx, types.NamespacedName{Name: "app8", Namespace: "default"}, &got); err != nil {
			return false
		}
		var sec corev1.Secret
		secErr := c.Get(ctx, types.NamespacedName{Name: "app8-token", Namespace: "default"}, &sec)
		return meta.IsStatusConditionTrue(got.Status.Conditions, controller.CondReady) &&
			got.Status.InviteID != "" && secErr == nil && len(sec.Data["token"]) > 0
	})
}

func TestReconcileDeletePolicy(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, hub, stop := startManager(t)
	defer stop()
	ctx := t.Context()
	for _, policy := range []towonelv1alpha1.DeletionPolicy{towonelv1alpha1.DeletionPolicyDelete, towonelv1alpha1.DeletionPolicyRetain} {
		name := "del-" + strings.ToLower(string(policy))
		tt := &towonelv1alpha1.TowonelTunnel{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       towonelv1alpha1.TowonelTunnelSpec{DeletionPolicy: policy},
		}
		if err := c.Create(ctx, tt); err != nil {
			t.Fatal(err)
		}
		var inviteID string
		waitFor(t, 15*time.Second, func() bool {
			var g towonelv1alpha1.TowonelTunnel
			if c.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &g) != nil {
				return false
			}
			inviteID = g.Status.InviteID
			return inviteID != ""
		})
		if err := c.Delete(ctx, tt); err != nil {
			t.Fatal(err)
		}
		waitFor(t, 15*time.Second, func() bool {
			var g towonelv1alpha1.TowonelTunnel
			return apierrors.IsNotFound(c.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, &g))
		})
		gone := !hub.Has(inviteID)
		if policy == towonelv1alpha1.DeletionPolicyDelete && !gone {
			t.Errorf("Delete policy: invite %s should be gone", inviteID)
		}
		if policy == towonelv1alpha1.DeletionPolicyRetain && gone {
			t.Errorf("Retain policy: invite %s should remain", inviteID)
		}
	}
}

func TestTunnelAggregatesAgents(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, hub, stop := startManager(t)
	defer stop()
	ctx := t.Context()
	tt := &towonelv1alpha1.TowonelTunnel{
		ObjectMeta: metav1.ObjectMeta{Name: "agg", Namespace: "default"},
		Spec:       towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"extra.example"}},
	}
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-a", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{
			TunnelRef: towonelv1alpha1.TunnelReference{Name: "agg"},
			Services:  []towonelv1alpha1.AgentService{{Hostname: "app.example", Origin: "app:8080"}},
			TCP:       []towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "app:22", PreferredPort: 2222}},
		},
	}
	if err := c.Create(ctx, ta); err != nil {
		t.Fatal(err)
	}
	var tenant string
	waitFor(t, 15*time.Second, func() bool {
		var got towonelv1alpha1.TowonelTunnel
		if c.Get(ctx, types.NamespacedName{Name: "agg", Namespace: "default"}, &got) != nil {
			return false
		}
		tenant = got.Status.TenantID
		return slices.Contains(got.Status.AuthorizedHostnames, "app.example") &&
			slices.Contains(got.Status.AuthorizedHostnames, "extra.example") &&
			len(got.Status.PortAllocations) == 1 &&
			got.Status.PortAllocations[0].ListenPort == 2222 &&
			meta.IsStatusConditionTrue(got.Status.Conditions, controller.CondPortsReserved)
	})
	// Agent deleted -> hostname deauthorized + port released (no agent finalizer needed).
	if err := c.Delete(ctx, ta); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, func() bool {
		var got towonelv1alpha1.TowonelTunnel
		if c.Get(ctx, types.NamespacedName{Name: "agg", Namespace: "default"}, &got) != nil {
			return false
		}
		return !slices.Contains(got.Status.AuthorizedHostnames, "app.example") &&
			len(got.Status.PortAllocations) == 0 && hub.ReservationCount(tenant) == 0
	})
}
