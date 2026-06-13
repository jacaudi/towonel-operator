package controller

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// Agent-flavored analogs of the tunnel's setCond/writeStatus/fail. Two
// type-distinct copies; a generic helper is deferred until the shape proves
// stable (Rule of Three / wrong-abstraction guard, design §4.G).

func setAgentCond(ta *towonelv1alpha1.TowonelAgent, condType string, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(&ta.Status.Conditions, metav1.Condition{
		Type: condType, Status: status, Reason: reason, Message: msg, ObservedGeneration: ta.Generation,
	})
}

// markAgentWaiting sets the WaitingForTunnel phase (TunnelReady already False).
func markAgentWaiting(ta *towonelv1alpha1.TowonelAgent) {
	ta.Status.Phase = "WaitingForTunnel"
}

// rollupAgentStatus derives PortsAllocated/WorkloadAvailable + phase.
// Phase Ready requires ALL FOUR conditions true (design §4.G).
// dep may be nil (treated as unavailable).
func rollupAgentStatus(ta *towonelv1alpha1.TowonelAgent, cfg agentConfig, dep *appsv1.Deployment) {
	switch {
	case len(ta.Spec.TCP)+len(ta.Spec.UDP) == 0:
		setAgentCond(ta, CondPortsAllocated, metav1.ConditionTrue, ReasonNoL4Services, "no tcp/udp services declared")
	case len(cfg.Pending) > 0:
		setAgentCond(ta, CondPortsAllocated, metav1.ConditionFalse, ReasonPending,
			fmt.Sprintf("awaiting port allocations: %s", strings.Join(cfg.Pending, ", ")))
	default:
		setAgentCond(ta, CondPortsAllocated, metav1.ConditionTrue, ReasonSynced, "all l4 services allocated")
	}

	available := false
	if dep != nil {
		for _, c := range dep.Status.Conditions {
			if c.Type == appsv1.DeploymentAvailable && c.Status == corev1.ConditionTrue {
				available = true
			}
		}
	}
	if available {
		setAgentCond(ta, CondWorkloadAvailable, metav1.ConditionTrue, ReasonAvailable, "deployment available")
	} else {
		setAgentCond(ta, CondWorkloadAvailable, metav1.ConditionFalse, ReasonUnavailable, "deployment not yet available")
	}

	ta.Status.ObservedConfigHash = cfg.hash()

	if !meta.IsStatusConditionTrue(ta.Status.Conditions, CondTunnelReady) {
		markAgentWaiting(ta)
		return
	}
	allTrue := true
	for _, c := range []string{CondTunnelReady, CondConfigRendered, CondPortsAllocated, CondWorkloadAvailable} {
		if !meta.IsStatusConditionTrue(ta.Status.Conditions, c) {
			allTrue = false
		}
	}
	if allTrue {
		ta.Status.Phase = "Ready"
	} else {
		ta.Status.Phase = "Pending"
	}
}

// writeStatus persists agent status via read-modify-write, gated on a
// semantic diff (mirrors the tunnel's writeStatus).
func (r *TowonelAgentReconciler) writeStatus(ctx context.Context, ta *towonelv1alpha1.TowonelAgent, orig *towonelv1alpha1.TowonelAgentStatus) error {
	ta.Status.ObservedGeneration = ta.Generation
	if equality.Semantic.DeepEqual(orig, &ta.Status) {
		return nil
	}
	return r.Status().Update(ctx, ta)
}

// setConnectivityCond sets IrohConnectivityReady (design §8). It is deliberately
// NOT part of the rollupAgentStatus allTrue set — connectivity is optional and
// must never gate phase Ready. Absent when connectivity is unrequested and there
// is nothing to report.
func setConnectivityCond(ta *towonelv1alpha1.TowonelAgent, p connectivityPlan, requested, shellMissing bool) {
	switch {
	case p.skipped:
		setAgentCond(ta, CondIrohConnectivityReady, metav1.ConditionFalse, ReasonConnectivitySkipped, p.skipReason)
	case shellMissing:
		setAgentCond(ta, CondIrohConnectivityReady, metav1.ConditionFalse, ReasonNodeRBACShellMissing,
			"chart node-RBAC shell missing; enable agentNodeRBAC.create in the chart")
	case !requested:
		meta.RemoveStatusCondition(&ta.Status.Conditions, CondIrohConnectivityReady)
	default:
		setAgentCond(ta, CondIrohConnectivityReady, metav1.ConditionTrue, ReasonConnectivityReady, "direct-path connectivity applied")
	}
}

// fail sets ConfigRendered=False and persists status defensively.
// ReasonReconciling, not APIError: this controller makes zero hub calls —
// errors here are kube-API/render failures.
func (r *TowonelAgentReconciler) fail(ctx context.Context, ta *towonelv1alpha1.TowonelAgent, orig *towonelv1alpha1.TowonelAgentStatus, err error) (ctrl.Result, error) {
	setAgentCond(ta, CondConfigRendered, metav1.ConditionFalse, ReasonReconciling, err.Error())
	ta.Status.Phase = "Pending"
	_ = r.writeStatus(ctx, ta, orig) // defensive; surface the real error
	return ctrl.Result{}, err
}
