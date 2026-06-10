package envtest_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/controller"
)

// markDeploymentAvailable simulates kube-controller-manager (absent in
// envtest): Deployments never gain Available on their own (design §5 note).
func markDeploymentAvailable(t *testing.T, c client.Client, nn types.NamespacedName) {
	t.Helper()
	waitFor(t, 15*time.Second, func() bool {
		var dep appsv1.Deployment
		if c.Get(t.Context(), nn, &dep) != nil {
			return false
		}
		dep.Status.Conditions = []appsv1.DeploymentCondition{{
			Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue,
			Reason: "MinimumReplicasAvailable", LastTransitionTime: metav1.Now(),
		}}
		dep.Status.Replicas, dep.Status.ReadyReplicas = 1, 1
		return c.Status().Update(t.Context(), &dep) == nil
	})
}

func agentEnvValue(dep *appsv1.Deployment, name string) (string, bool) {
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

func TestAgentRoundTrip(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()

	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "rt", Namespace: "default"}}
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "rt-edge", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{
			TunnelRef: towonelv1alpha1.TunnelReference{Name: "rt"},
			Services:  []towonelv1alpha1.AgentService{{Hostname: "rt.example", Origin: "rt:8080"}},
			TCP:       []towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "rt:22", PreferredPort: 2222}},
		},
	}
	if err := c.Create(ctx, ta); err != nil {
		t.Fatal(err)
	}

	// Deployment renders with listen_port from the tunnel's allocation.
	depNN := types.NamespacedName{Name: "rt-edge", Namespace: "default"}
	waitFor(t, 20*time.Second, func() bool {
		var dep appsv1.Deployment
		if c.Get(ctx, depNN, &dep) != nil {
			return false
		}
		v, ok := agentEnvValue(&dep, "TOWONEL_AGENT_TCP_SERVICES")
		return ok && strings.Contains(v, `"listen_port":2222`)
	})
	// Agent secret copied into the agent namespace with the token bytes.
	var agentSec corev1.Secret
	waitFor(t, 15*time.Second, func() bool {
		return c.Get(ctx, types.NamespacedName{Name: "rt-edge-token", Namespace: "default"}, &agentSec) == nil &&
			len(agentSec.Data["token"]) > 0
	})

	markDeploymentAvailable(t, c, depNN)
	waitFor(t, 15*time.Second, func() bool {
		var got towonelv1alpha1.TowonelAgent
		if c.Get(ctx, types.NamespacedName{Name: "rt-edge", Namespace: "default"}, &got) != nil {
			return false
		}
		return got.Status.Phase == "Ready" &&
			meta.IsStatusConditionTrue(got.Status.Conditions, controller.CondTunnelReady) &&
			meta.IsStatusConditionTrue(got.Status.Conditions, controller.CondConfigRendered) &&
			meta.IsStatusConditionTrue(got.Status.Conditions, controller.CondPortsAllocated) &&
			meta.IsStatusConditionTrue(got.Status.Conditions, controller.CondWorkloadAvailable) &&
			got.Status.ObservedConfigHash != ""
	})
	// Children carry controller ownerRefs (envtest has no GC controller).
	var dep appsv1.Deployment
	if err := c.Get(ctx, depNN, &dep); err != nil {
		t.Fatal(err)
	}
	if metav1.GetControllerOf(&dep) == nil || metav1.GetControllerOf(&agentSec) == nil {
		t.Fatal("children must be controller-owned by the agent")
	}
}

func TestAgentWaitsForTunnelThenRenders(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()

	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "wait-edge", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{
			TunnelRef: towonelv1alpha1.TunnelReference{Name: "wait"},
			Services:  []towonelv1alpha1.AgentService{{Hostname: "w.example", Origin: "w:80"}},
		},
	}
	if err := c.Create(ctx, ta); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, func() bool {
		var got towonelv1alpha1.TowonelAgent
		return c.Get(ctx, types.NamespacedName{Name: "wait-edge", Namespace: "default"}, &got) == nil &&
			got.Status.Phase == "WaitingForTunnel"
	})
	var dep appsv1.Deployment
	if err := c.Get(ctx, types.NamespacedName{Name: "wait-edge", Namespace: "default"}, &dep); err == nil {
		t.Fatal("no Deployment may exist while WaitingForTunnel")
	}

	// Tunnel arrives -> the watch (not the 30s fallback) wakes the agent fast.
	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "wait", Namespace: "default"}}
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, func() bool {
		return c.Get(ctx, types.NamespacedName{Name: "wait-edge", Namespace: "default"}, &dep) == nil
	})
}

func TestTwoAgentsShareTunnel(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, hub, stop := startManager(t)
	defer stop()
	ctx := t.Context()

	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "share", Namespace: "default"}}
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"share-a", "share-b"} {
		ta := &towonelv1alpha1.TowonelAgent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: towonelv1alpha1.TowonelAgentSpec{
				TunnelRef: towonelv1alpha1.TunnelReference{Name: "share"},
				Services:  []towonelv1alpha1.AgentService{{Hostname: "shared.example", Origin: name + ":80"}},
				TCP:       []towonelv1alpha1.AgentL4Service{{Name: "game", Origin: name + ":4086"}},
			},
		}
		if err := c.Create(ctx, ta); err != nil {
			t.Fatal(err)
		}
	}
	var tenant string
	waitFor(t, 20*time.Second, func() bool {
		var got towonelv1alpha1.TowonelTunnel
		if c.Get(ctx, types.NamespacedName{Name: "share", Namespace: "default"}, &got) != nil {
			return false
		}
		tenant = got.Status.TenantID
		// ONE shared reservation; hostname deduped to one entry.
		return len(got.Status.PortAllocations) == 1 && tenant != "" && hub.ReservationCount(tenant) == 1
	})
	// Delete one agent: shared port + hostname survive (other agent still declares them).
	if err := c.Delete(ctx, &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "share-a", Namespace: "default"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, func() bool { // shrink the vacuous window: deletion observed first
		var gone towonelv1alpha1.TowonelAgent
		return c.Get(ctx, types.NamespacedName{Name: "share-a", Namespace: "default"}, &gone) != nil
	})
	time.Sleep(2 * time.Second) // allow re-aggregation to run after the delete event
	var got towonelv1alpha1.TowonelTunnel
	if err := c.Get(ctx, types.NamespacedName{Name: "share", Namespace: "default"}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.PortAllocations) != 1 || hub.ReservationCount(tenant) != 1 {
		t.Fatalf("shared reservation must survive one deletion: status=%d hub=%d",
			len(got.Status.PortAllocations), hub.ReservationCount(tenant))
	}
	if !slices.Contains(got.Status.AuthorizedHostnames, "shared.example") {
		t.Fatal("shared hostname must survive one deletion")
	}
}

func TestAgentRotationRollsDeployment(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()

	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "rot", Namespace: "default"}}
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "rot-edge", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{
			TunnelRef: towonelv1alpha1.TunnelReference{Name: "rot"},
			Services:  []towonelv1alpha1.AgentService{{Hostname: "rot.example", Origin: "rot:80"}},
		},
	}
	if err := c.Create(ctx, ta); err != nil {
		t.Fatal(err)
	}
	var hashBefore string
	waitFor(t, 20*time.Second, func() bool {
		var got towonelv1alpha1.TowonelAgent
		if c.Get(ctx, types.NamespacedName{Name: "rot-edge", Namespace: "default"}, &got) != nil {
			return false
		}
		hashBefore = got.Status.ObservedConfigHash
		return hashBefore != ""
	})

	// Simulate rotation: new invite-id + token on the tunnel's Secret AND status
	// (the rotation phase will do this for real; the agent must consume it).
	var tunnelSec corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Name: "rot-token", Namespace: "default"}, &tunnelSec); err != nil {
		t.Fatal(err)
	}
	tunnelSec.Data["token"] = []byte("tok-rotated")
	tunnelSec.Annotations[controller.AnnotationInviteID] = "inv-rotated"
	if err := c.Update(ctx, &tunnelSec); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, func() bool { // retry on conflict (tunnel controller also writes status)
		var tun towonelv1alpha1.TowonelTunnel
		if c.Get(ctx, types.NamespacedName{Name: "rot", Namespace: "default"}, &tun) != nil {
			return false
		}
		tun.Status.InviteID = "inv-rotated"
		return c.Status().Update(ctx, &tun) == nil
	})

	waitFor(t, 45*time.Second, func() bool { // covers the 30s staleness fallback
		var sec corev1.Secret
		if c.Get(ctx, types.NamespacedName{Name: "rot-edge-token", Namespace: "default"}, &sec) != nil {
			return false
		}
		if string(sec.Data["token"]) != "tok-rotated" { // TOKEN BYTES, not just hash
			return false
		}
		var got towonelv1alpha1.TowonelAgent
		return c.Get(ctx, types.NamespacedName{Name: "rot-edge", Namespace: "default"}, &got) == nil &&
			got.Status.ObservedConfigHash != hashBefore
	})
}

func TestAgentNoChurn(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()

	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "calm", Namespace: "default"}}
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "calm-edge", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{
			TunnelRef: towonelv1alpha1.TunnelReference{Name: "calm"},
			Services:  []towonelv1alpha1.AgentService{{Hostname: "calm.example", Origin: "calm:80"}},
		},
	}
	if err := c.Create(ctx, ta); err != nil {
		t.Fatal(err)
	}
	depNN := types.NamespacedName{Name: "calm-edge", Namespace: "default"}
	var dep appsv1.Deployment
	waitFor(t, 20*time.Second, func() bool { return c.Get(ctx, depNN, &dep) == nil })
	gen, rv := dep.Generation, dep.ResourceVersion
	secNN := types.NamespacedName{Name: "calm-edge-token", Namespace: "default"}
	var sec corev1.Secret
	waitFor(t, 15*time.Second, func() bool { return c.Get(ctx, secNN, &sec) == nil })
	secRV := sec.ResourceVersion
	var agent towonelv1alpha1.TowonelAgent
	agentNN := types.NamespacedName{Name: "calm-edge", Namespace: "default"}
	// Wait for the agent status to settle (a status write may still be in flight).
	var agentRV string
	waitFor(t, 15*time.Second, func() bool {
		if c.Get(ctx, agentNN, &agent) != nil {
			return false
		}
		if agent.Status.ObservedConfigHash == "" {
			return false
		}
		agentRV = agent.ResourceVersion
		return true
	})

	time.Sleep(3 * time.Second) // several reconcile opportunities (watch echoes)
	if err := c.Get(ctx, depNN, &dep); err != nil {
		t.Fatal(err)
	}
	if dep.Generation != gen || dep.ResourceVersion != rv {
		t.Fatalf("deployment churned: gen %d->%d rv %s->%s", gen, dep.Generation, rv, dep.ResourceVersion)
	}
	if err := c.Get(ctx, secNN, &sec); err != nil {
		t.Fatal(err)
	}
	if sec.ResourceVersion != secRV {
		t.Fatal("agent secret churned")
	}
	if err := c.Get(ctx, agentNN, &agent); err != nil {
		t.Fatal(err)
	}
	if agent.ResourceVersion != agentRV {
		t.Fatal("agent status churned (spurious status write)")
	}
}

func TestCrossNamespaceTunnelRef(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "p4-net"}}
	if err := c.Create(ctx, ns); err != nil && !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "xns", Namespace: "p4-net"}}
	if err := c.Create(ctx, tt); err != nil {
		t.Fatal(err)
	}
	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "xns-edge", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{
			TunnelRef: towonelv1alpha1.TunnelReference{Name: "xns", Namespace: "p4-net"},
			Services:  []towonelv1alpha1.AgentService{{Hostname: "xns.example", Origin: "x:80"}},
		},
	}
	if err := c.Create(ctx, ta); err != nil {
		t.Fatal(err)
	}
	// Agent secret lands in the AGENT's namespace; tunnel aggregated the hostname.
	waitFor(t, 20*time.Second, func() bool {
		var sec corev1.Secret
		if c.Get(ctx, types.NamespacedName{Name: "xns-edge-token", Namespace: "default"}, &sec) != nil {
			return false
		}
		var got towonelv1alpha1.TowonelTunnel
		if c.Get(ctx, types.NamespacedName{Name: "xns", Namespace: "p4-net"}, &got) != nil {
			return false
		}
		return slices.Contains(got.Status.AuthorizedHostnames, "xns.example")
	})
}

func TestAgentRetargetsTunnel(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, hub, stop := startManager(t)
	defer stop()
	ctx := t.Context()

	for _, name := range []string{"ret-a", "ret-b"} {
		tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}}
		if err := c.Create(ctx, tt); err != nil {
			t.Fatal(err)
		}
	}
	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "ret-edge", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{
			TunnelRef: towonelv1alpha1.TunnelReference{Name: "ret-a"},
			Services:  []towonelv1alpha1.AgentService{{Hostname: "ret.example", Origin: "r:80"}},
			TCP:       []towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "r:22"}},
		},
	}
	if err := c.Create(ctx, ta); err != nil {
		t.Fatal(err)
	}
	var tenantA string
	waitFor(t, 20*time.Second, func() bool {
		var a towonelv1alpha1.TowonelTunnel
		if c.Get(ctx, types.NamespacedName{Name: "ret-a", Namespace: "default"}, &a) != nil {
			return false
		}
		tenantA = a.Status.TenantID
		return slices.Contains(a.Status.AuthorizedHostnames, "ret.example") && len(a.Status.PortAllocations) == 1
	})

	// Retarget tunnelRef A -> B: the update event maps BOTH tunnels (design §3.3).
	waitFor(t, 15*time.Second, func() bool { // retry on conflict
		var cur towonelv1alpha1.TowonelAgent
		if c.Get(ctx, types.NamespacedName{Name: "ret-edge", Namespace: "default"}, &cur) != nil {
			return false
		}
		cur.Spec.TunnelRef.Name = "ret-b"
		return c.Update(ctx, &cur) == nil
	})
	waitFor(t, 20*time.Second, func() bool {
		var a, b towonelv1alpha1.TowonelTunnel
		if c.Get(ctx, types.NamespacedName{Name: "ret-a", Namespace: "default"}, &a) != nil ||
			c.Get(ctx, types.NamespacedName{Name: "ret-b", Namespace: "default"}, &b) != nil {
			return false
		}
		// Old tunnel pruned; new tunnel acquired.
		return !slices.Contains(a.Status.AuthorizedHostnames, "ret.example") &&
			len(a.Status.PortAllocations) == 0 && hub.ReservationCount(tenantA) == 0 &&
			slices.Contains(b.Status.AuthorizedHostnames, "ret.example") &&
			len(b.Status.PortAllocations) == 1
	})
}

func TestTunnelDeleteReleasesAgentPorts(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, hub, stop := startManager(t)
	defer stop()
	ctx := t.Context()

	for _, tc := range []struct {
		name   string
		policy towonelv1alpha1.DeletionPolicy
		gone   bool
	}{
		{"pdel", towonelv1alpha1.DeletionPolicyDelete, true},
		{"pret", towonelv1alpha1.DeletionPolicyRetain, false},
	} {
		tt := &towonelv1alpha1.TowonelTunnel{
			ObjectMeta: metav1.ObjectMeta{Name: tc.name, Namespace: "default"},
			Spec:       towonelv1alpha1.TowonelTunnelSpec{DeletionPolicy: tc.policy},
		}
		if err := c.Create(ctx, tt); err != nil {
			t.Fatal(err)
		}
		ta := &towonelv1alpha1.TowonelAgent{
			ObjectMeta: metav1.ObjectMeta{Name: tc.name + "-edge", Namespace: "default"},
			Spec: towonelv1alpha1.TowonelAgentSpec{
				TunnelRef: towonelv1alpha1.TunnelReference{Name: tc.name},
				TCP:       []towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "o:22"}},
			},
		}
		if err := c.Create(ctx, ta); err != nil {
			t.Fatal(err)
		}
		var tenant string
		waitFor(t, 20*time.Second, func() bool {
			var got towonelv1alpha1.TowonelTunnel
			if c.Get(ctx, types.NamespacedName{Name: tc.name, Namespace: "default"}, &got) != nil {
				return false
			}
			tenant = got.Status.TenantID
			return len(got.Status.PortAllocations) == 1
		})
		if err := c.Delete(ctx, tt); err != nil {
			t.Fatal(err)
		}
		waitFor(t, 20*time.Second, func() bool {
			var got towonelv1alpha1.TowonelTunnel
			return c.Get(ctx, types.NamespacedName{Name: tc.name, Namespace: "default"}, &got) != nil
		})
		if released := hub.ReservationCount(tenant) == 0; released != tc.gone {
			t.Errorf("%s policy: reservations released = %v, want %v", tc.policy, released, tc.gone)
		}
	}
}
