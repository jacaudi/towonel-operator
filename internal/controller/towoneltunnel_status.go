package controller

import (
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func setCond(tt *towonelv1alpha1.TowonelTunnel, condType string, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&tt.Status.Conditions, metav1.Condition{
		Type: condType, Status: status, Reason: reason, Message: msg, ObservedGeneration: tt.Generation,
	})
}

// rollupStatus derives InviteIssued/TokenExpiringSoon/Ready + phase. `now` is injectable.
func rollupStatus(tt *towonelv1alpha1.TowonelTunnel, now time.Time) {
	if tt.Status.InviteID != "" {
		setCond(tt, CondInviteIssued, metav1.ConditionTrue, ReasonSynced, "invite issued")
	}
	expiringSoon := tt.Spec.TokenExpiry != 0 && tt.Status.ExpiresAt != 0 &&
		time.UnixMilli(tt.Status.ExpiresAt).Sub(now) <= renewWindow
	if expiringSoon {
		setCond(tt, CondTokenExpiringSoon, metav1.ConditionTrue, ReasonExpiringSoon, "token nearing expiry")
	} else {
		setCond(tt, CondTokenExpiringSoon, metav1.ConditionFalse, ReasonNotExpiring, "token not nearing expiry")
	}
	if tt.Status.InviteID != "" {
		setCond(tt, CondReady, metav1.ConditionTrue, ReasonReady, "tunnel reconciled")
		tt.Status.Phase = "Ready"
	} else {
		setCond(tt, CondReady, metav1.ConditionFalse, ReasonReconciling, "invite not yet issued")
		tt.Status.Phase = "Pending"
	}
}
