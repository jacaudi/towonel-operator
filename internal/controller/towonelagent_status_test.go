package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func availableDep(avail bool) *appsv1.Deployment {
	status := corev1.ConditionFalse
	if avail {
		status = corev1.ConditionTrue
	}
	return &appsv1.Deployment{Status: appsv1.DeploymentStatus{
		Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: status}},
	}}
}

func TestRollupAgentStatus(t *testing.T) {
	tests := []struct {
		name            string
		tcp             []towonelv1alpha1.AgentL4Service
		pending         []string
		available       bool
		tunnelReady     bool
		configRendered  bool
		wantPhase       string
		wantPorts       metav1.ConditionStatus
		wantPortsReason string
	}{
		{"https-only ready (vacuous PortsAllocated)", nil, nil, true, true, true, "Ready", metav1.ConditionTrue, ReasonNoL4Services},
		{"pending ports -> Pending phase", []towonelv1alpha1.AgentL4Service{{Name: "x", Origin: "o:1"}}, []string{"tcp/x"}, true, true, true, "Pending", metav1.ConditionFalse, ReasonPending},
		{"workload unavailable -> Pending", nil, nil, false, true, true, "Pending", metav1.ConditionTrue, ReasonNoL4Services},
		{"config not rendered -> Pending", nil, nil, true, true, false, "Pending", metav1.ConditionTrue, ReasonNoL4Services},
		{"tunnel not ready -> WaitingForTunnel (hash still stamped)", nil, nil, true, false, true, "WaitingForTunnel", metav1.ConditionTrue, ReasonNoL4Services},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ta := &towonelv1alpha1.TowonelAgent{Spec: towonelv1alpha1.TowonelAgentSpec{TCP: tc.tcp}}
			if tc.tunnelReady {
				setAgentCond(ta, CondTunnelReady, metav1.ConditionTrue, ReasonReady, "ok")
			} else {
				setAgentCond(ta, CondTunnelReady, metav1.ConditionFalse, ReasonTunnelNotFound, "missing")
			}
			if tc.configRendered {
				setAgentCond(ta, CondConfigRendered, metav1.ConditionTrue, ReasonRendered, "ok")
			} else {
				setAgentCond(ta, CondConfigRendered, metav1.ConditionFalse, ReasonReconciling, "rendering")
			}
			cfg := agentConfig{Pending: tc.pending}
			cfg.InviteID = "inv-1"
			rollupAgentStatus(ta, cfg, availableDep(tc.available))
			if ta.Status.Phase != tc.wantPhase {
				t.Errorf("phase = %s, want %s", ta.Status.Phase, tc.wantPhase)
			}
			got := meta.FindStatusCondition(ta.Status.Conditions, CondPortsAllocated)
			if got == nil || got.Status != tc.wantPorts {
				t.Errorf("PortsAllocated = %+v, want %s", got, tc.wantPorts)
			}
			if got == nil || got.Reason != tc.wantPortsReason {
				t.Errorf("PortsAllocated reason = %+v, want %s", got, tc.wantPortsReason)
			}
			if ta.Status.ObservedConfigHash != cfg.hash() {
				t.Error("observedConfigHash not mirrored")
			}
		})
	}
}

// TestRollupAgentStatusNilDep exercises the nil-dep guard: a nil Deployment
// is treated as unavailable, not a panic.
func TestRollupAgentStatusNilDep(t *testing.T) {
	ta := &towonelv1alpha1.TowonelAgent{}
	setAgentCond(ta, CondTunnelReady, metav1.ConditionTrue, ReasonReady, "ok")
	setAgentCond(ta, CondConfigRendered, metav1.ConditionTrue, ReasonRendered, "ok")
	cfg := agentConfig{InviteID: "inv-1"}
	rollupAgentStatus(ta, cfg, nil)
	if got := meta.FindStatusCondition(ta.Status.Conditions, CondWorkloadAvailable); got == nil || got.Status != metav1.ConditionFalse {
		t.Errorf("WorkloadAvailable = %+v, want False", got)
	}
	if ta.Status.Phase != "Pending" {
		t.Errorf("phase = %s, want Pending", ta.Status.Phase)
	}
}

func TestAgentPhaseWaitingForTunnel(t *testing.T) {
	ta := &towonelv1alpha1.TowonelAgent{}
	setAgentCond(ta, CondTunnelReady, metav1.ConditionFalse, ReasonTunnelNotFound, "missing")
	markAgentWaiting(ta)
	if ta.Status.Phase != "WaitingForTunnel" {
		t.Errorf("phase = %s", ta.Status.Phase)
	}
}
