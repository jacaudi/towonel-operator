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
