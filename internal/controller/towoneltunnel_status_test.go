package controller

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
