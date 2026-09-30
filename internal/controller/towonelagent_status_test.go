package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func deploymentEnv(dep *appsv1.Deployment, name string) (string, bool) {
	if dep == nil || len(dep.Spec.Template.Spec.Containers) == 0 {
		return "", false
	}
	for _, env := range dep.Spec.Template.Spec.Containers[0].Env {
		if env.Name == name {
			return env.Value, true
		}
	}
	return "", false
}

func availableDep(avail bool) *appsv1.Deployment {
	status := corev1.ConditionFalse
	if avail {
		status = corev1.ConditionTrue
	}
	return &appsv1.Deployment{Status: appsv1.DeploymentStatus{
		Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: status}},
	}}
}

func TestAgentStatusWriteConflictPreservesNewerStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := towonelv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	agent := &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "default", Generation: 2}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(agent).WithObjects(agent).Build()
	key := types.NamespacedName{Namespace: "default", Name: "edge"}
	var stale towonelv1alpha1.TowonelAgent
	if err := cl.Get(t.Context(), key, &stale); err != nil {
		t.Fatal(err)
	}
	var current towonelv1alpha1.TowonelAgent
	if err := cl.Get(t.Context(), key, &current); err != nil {
		t.Fatal(err)
	}
	current.Status.Phase = "Ready"
	current.Status.ObservedGeneration = 2
	current.Status.Conditions = []metav1.Condition{{Type: CondConfigRendered, Status: metav1.ConditionTrue, Reason: ReasonRendered, ObservedGeneration: 2}}
	if err := cl.Status().Update(t.Context(), &current); err != nil {
		t.Fatal(err)
	}
	stale.Generation = 2
	stale.Status.Phase = "Pending"
	stale.Status.ObservedGeneration = stale.Generation
	stale.ResourceVersion = "stale-resource-version"
	r := &TowonelAgentReconciler{Client: cl}
	if err := r.writeStatus(t.Context(), &stale, &towonelv1alpha1.TowonelAgentStatus{}); !apierrors.IsConflict(err) {
		t.Fatalf("writeStatus error = %v, want conflict", err)
	}
	var got towonelv1alpha1.TowonelAgent
	if err := cl.Get(t.Context(), key, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != "Ready" || got.Status.ObservedGeneration != 2 {
		t.Fatalf("status = %+v, want concurrent Ready at generation 2", got.Status)
	}
}

func TestAgentReconcileRetriesWithLatestSpecAfterStatusConflict(t *testing.T) {
	scheme := agentScheme(t)
	tunnel, tunnelToken := tunnelWithToken("inv-1", "inv-1", "tok")
	tunnel.Status.PortAllocations = nil
	tunnel.ResourceVersion = "1"
	agent := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "default", UID: "agent-uid", ResourceVersion: "1"},
		Spec: towonelv1alpha1.TowonelAgentSpec{
			TunnelRef: towonelv1alpha1.TunnelReference{Name: "app", Namespace: "network"},
			Services:  []towonelv1alpha1.AgentService{{Hostname: "old.example", Origin: "old:80", EdgeTLSMode: "passthrough"}},
		},
	}
	store := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(agent, tunnel).
		WithObjects(agent, tunnel, tunnelToken).Build()
	writeCalls := 0
	var injectedSpecUpdate bool
	cl := interceptor.NewClient(store, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if subresource == "status" {
				writeCalls++
				if writeCalls == 1 {
					var latest towonelv1alpha1.TowonelAgent
					if err := store.Get(ctx, client.ObjectKeyFromObject(obj), &latest); err != nil {
						return err
					}
					latest.Spec.Services = []towonelv1alpha1.AgentService{{Hostname: "new.example", Origin: "new:80", EdgeTLSMode: "passthrough"}}
					latest.Generation = 2
					if err := store.Update(ctx, &latest); err != nil {
						return err
					}
					injectedSpecUpdate = true
					return apierrors.NewConflict(towonelv1alpha1.GroupVersion.WithResource("towonelagents").GroupResource(), obj.GetName(), errors.New("injected spec race"))
				}
			}
			return c.SubResource(subresource).Update(ctx, obj, opts...)
		},
	})
	// Fake clients do not allocate versions; the interceptor simulates the API's
	// spec-update conflict and lets the retry fetch the updated object.
	r := &TowonelAgentReconciler{Client: cl, APIReader: store, Scheme: scheme}
	_, err := r.Reconcile(t.Context(), reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "edge"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !injectedSpecUpdate || writeCalls < 2 {
		t.Fatalf("status writes = %d, injected update = %t; want a conflict and retried status write", writeCalls, injectedSpecUpdate)
	}
	var gotAgent towonelv1alpha1.TowonelAgent
	if err := store.Get(t.Context(), client.ObjectKeyFromObject(agent), &gotAgent); err != nil {
		t.Fatal(err)
	}
	var gotDeployment appsv1.Deployment
	if err := store.Get(t.Context(), client.ObjectKey{Namespace: "default", Name: "edge"}, &gotDeployment); err != nil {
		t.Fatal(err)
	}
	servicesEnv, ok := deploymentEnv(&gotDeployment, "TOWONEL_AGENT_SERVICES")
	if !ok || !strings.Contains(servicesEnv, `"new.example"`) || strings.Contains(servicesEnv, `"old.example"`) {
		t.Fatalf("deployment services = %q; want only latest spec", servicesEnv)
	}
	if gotAgent.Status.ObservedConfigHash != gotDeployment.Spec.Template.Annotations[AnnotationConfigHash] || gotAgent.Status.ObservedGeneration != gotAgent.Generation {
		t.Fatalf("status hash/generation = %q/%d, deployment hash = %q, generation = %d", gotAgent.Status.ObservedConfigHash, gotAgent.Status.ObservedGeneration, gotDeployment.Spec.Template.Annotations[AnnotationConfigHash], gotAgent.Generation)
	}
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

func TestConnectivityCondDoesNotGateReady(t *testing.T) {
	ta := &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "n"}}
	setAgentCond(ta, CondTunnelReady, metav1.ConditionTrue, ReasonReady, "")
	setAgentCond(ta, CondConfigRendered, metav1.ConditionTrue, ReasonRendered, "")
	dep := &appsv1.Deployment{Status: appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue}}}}
	rollupAgentStatus(ta, agentConfig{}, dep)
	setConnectivityCond(ta, connectivityPlan{skipped: true, skipReason: "bad"}, false, false)
	if ta.Status.Phase != "Ready" {
		t.Errorf("phase = %q; IrohConnectivityReady must not gate Ready", ta.Status.Phase)
	}
	if !meta.IsStatusConditionPresentAndEqual(ta.Status.Conditions, CondIrohConnectivityReady, metav1.ConditionFalse) {
		t.Error("IrohConnectivityReady should be False (skipped)")
	}
}

func TestConnectivityCondAbsentWhenUnrequested(t *testing.T) {
	ta := &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "n"}}
	setConnectivityCond(ta, connectivityPlan{}, false, false) // nothing requested
	if meta.FindStatusCondition(ta.Status.Conditions, CondIrohConnectivityReady) != nil {
		t.Error("condition must be absent when connectivity is unrequested")
	}
}
