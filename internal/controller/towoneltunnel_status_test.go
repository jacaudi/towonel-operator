package controller

import (
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func TestTokenExpiringSoon(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cases := []struct {
		name        string
		tokenExpiry int64
		expiresAt   int64
		want        metav1.ConditionStatus
	}{
		{"never", 0, 0, metav1.ConditionFalse},
		{"far", 3600, now.Add(30 * 24 * time.Hour).UnixMilli(), metav1.ConditionFalse},
		{"soon", 3600, now.Add(24 * time.Hour).UnixMilli(), metav1.ConditionTrue},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tt := &towonelv1alpha1.TowonelTunnel{}
			tt.Spec.TokenExpiry = c.tokenExpiry
			tt.Status.ExpiresAt = c.expiresAt
			rollupStatus(tt, now)
			got := meta.FindStatusCondition(tt.Status.Conditions, CondTokenExpiringSoon)
			if got == nil || got.Status != c.want {
				t.Fatalf("TokenExpiringSoon = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRollupPhase(t *testing.T) {
	tt := &towonelv1alpha1.TowonelTunnel{}
	tt.Status.InviteID = "inv-1"
	rollupStatus(tt, time.Unix(0, 0))
	if tt.Status.Phase != "Ready" {
		t.Errorf("phase = %q", tt.Status.Phase)
	}
	if !meta.IsStatusConditionTrue(tt.Status.Conditions, CondReady) {
		t.Error("Ready should be true")
	}
}

// TestHostnamesSyncedConditionTransition pins that CondHostnamesSynced and
// ReasonAPIError wire together correctly: a stale True flips to False when
// setCond is called with those constants (as the controller does on
// convergeHostnames failure).
func TestHostnamesSyncedConditionTransition(t *testing.T) {
	tt := &towonelv1alpha1.TowonelTunnel{}
	setCond(tt, CondHostnamesSynced, metav1.ConditionTrue, ReasonSynced, "authorized hostnames converged")
	if !meta.IsStatusConditionTrue(tt.Status.Conditions, CondHostnamesSynced) {
		t.Fatal("precondition: HostnamesSynced should be True")
	}
	// Simulate the failure branch in the controller.
	setCond(tt, CondHostnamesSynced, metav1.ConditionFalse, ReasonAPIError, "boom")
	if !meta.IsStatusConditionFalse(tt.Status.Conditions, CondHostnamesSynced) {
		t.Error("HostnamesSynced should be False after convergence failure")
	}
	got := meta.FindStatusCondition(tt.Status.Conditions, CondHostnamesSynced)
	if got == nil || got.Reason != ReasonAPIError {
		t.Errorf("reason = %q, want %q", got.Reason, ReasonAPIError)
	}
}

func TestTunnelAgentWatchFiltersStatusOnlyUpdates(t *testing.T) {
	p := crossWatchPredicate()
	old := &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "default", Generation: 1}}
	statusOnly := old.DeepCopy()
	statusOnly.ResourceVersion = "2"
	if p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: statusOnly}) {
		t.Fatal("status-only update should not enqueue tunnel reconciliation")
	}
	specChanged := old.DeepCopy()
	specChanged.Generation = 2
	if !p.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: specChanged}) {
		t.Fatal("generation update should pass")
	}
	if !p.Delete(event.DeleteEvent{Object: old}) {
		t.Fatal("delete should pass")
	}
	if !p.Create(event.CreateEvent{Object: old}) {
		t.Fatal("create should pass")
	}
}

func TestTunnelStatusWriteConflictPreservesNewerStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := towonelv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	tunnel := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "default", Generation: 2}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(tunnel).WithObjects(tunnel).Build()
	key := types.NamespacedName{Namespace: "default", Name: "edge"}
	var stale towonelv1alpha1.TowonelTunnel
	if err := cl.Get(t.Context(), key, &stale); err != nil {
		t.Fatal(err)
	}
	var current towonelv1alpha1.TowonelTunnel
	if err := cl.Get(t.Context(), key, &current); err != nil {
		t.Fatal(err)
	}
	current.Status.Phase = "Ready"
	current.Status.ObservedGeneration = 2
	current.Status.Conditions = []metav1.Condition{{Type: CondReady, Status: metav1.ConditionTrue, Reason: ReasonReady, ObservedGeneration: 2}}
	if err := cl.Status().Update(t.Context(), &current); err != nil {
		t.Fatal(err)
	}
	stale.Generation = 2
	stale.Status.Phase = "Pending"
	stale.Status.ObservedGeneration = stale.Generation
	stale.ResourceVersion = "stale-resource-version"
	r := &TowonelTunnelReconciler{Client: cl}
	if err := r.writeStatus(t.Context(), &stale, &towonelv1alpha1.TowonelTunnelStatus{}); !apierrors.IsConflict(err) {
		t.Fatalf("writeStatus error = %v, want conflict", err)
	}
	var got towonelv1alpha1.TowonelTunnel
	if err := cl.Get(t.Context(), key, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != "Ready" || got.Status.ObservedGeneration != 2 {
		t.Fatalf("status = %+v, want concurrent Ready at generation 2", got.Status)
	}
}
