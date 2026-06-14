package envtest_test

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/controller"
)

// nodeReader is the fixed name of the chart-owned shared node-reader
// ClusterRole + ClusterRoleBinding. The unexported controller.nodeReaderName
// isn't reachable from this package, so the literal is hardcoded here.
const nodeReader = "towonel-operator-agent-node-reader"

// mustCreate creates an object or fails the test.
func mustCreate(t *testing.T, c client.Client, obj client.Object) {
	t.Helper()
	if err := c.Create(t.Context(), obj); err != nil {
		t.Fatalf("create %T %s: %v", obj, obj.GetName(), err)
	}
}

// hasNodeReaderSubject reports whether the shared node-reader ClusterRoleBinding
// lists a ServiceAccount subject for the named agent. The binding is a
// cluster-scoped singleton that aggregates EVERY live autodiscover agent's SA
// across the whole suite (envtest has no GC, so agents from earlier tests
// linger in the `default` namespace). Tests therefore assert on their OWN
// subject's presence/absence rather than the binding's total subject count.
func hasNodeReaderSubject(c client.Client, ctx context.Context, name, namespace string) bool {
	var crb rbacv1.ClusterRoleBinding
	if c.Get(ctx, types.NamespacedName{Name: nodeReader}, &crb) != nil {
		return false
	}
	for _, s := range crb.Subjects {
		if s.Kind == "ServiceAccount" && s.Name == name && s.Namespace == namespace {
			return true
		}
	}
	return false
}

// installNodeReaderShell creates the chart-owned ClusterRole + ClusterRoleBinding
// shell that the operator's subjects reconcile RMW-patches. The chart is not
// installed in envtest, so tests that exercise the shared binding must create it.
//
// The shell is cluster-scoped (not namespaced), so it is a singleton shared
// across the whole suite. A prior test's t.Cleanup delete may not have
// propagated before the next test starts, so creation tolerates AlreadyExists
// and resets the binding's Subjects to empty for a clean starting state.
func installNodeReaderShell(t *testing.T, c client.Client) {
	t.Helper()
	ctx := t.Context()
	// Ensure a clean shell: wait until create succeeds or the existing object is
	// reset to zero subjects (tolerating a not-yet-propagated prior cleanup).
	waitFor(t, 15*time.Second, func() bool {
		cr := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: nodeReader},
			Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"get", "list", "watch"}}}}
		if err := c.Create(ctx, cr); err != nil && !apierrors.IsAlreadyExists(err) {
			return false
		}
		crb := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: nodeReader},
			RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: nodeReader}}
		if err := c.Create(ctx, crb); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return false
			}
			// Existing binding: reset subjects to empty so the test starts clean.
			var cur rbacv1.ClusterRoleBinding
			if c.Get(ctx, types.NamespacedName{Name: nodeReader}, &cur) != nil {
				return false
			}
			if len(cur.Subjects) == 0 {
				return true
			}
			cur.Subjects = nil
			return c.Update(ctx, &cur) == nil
		}
		return true
	})
	t.Cleanup(func() {
		_ = c.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: nodeReader}})
		_ = c.Delete(ctx, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: nodeReader}})
	})
}

func TestConnectivitySAAlwaysSet(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()
	mustCreate(t, c, &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "sa", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"sa.example"}}}) // #14(a): needs a hostname to issue an invite/token
	mustCreate(t, c, &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "sa-edge", Namespace: "default"},
		Spec:       towonelv1alpha1.TowonelAgentSpec{TunnelRef: towonelv1alpha1.TunnelReference{Name: "sa"}},
	})
	depNN := types.NamespacedName{Name: "sa-edge", Namespace: "default"}
	waitFor(t, 20*time.Second, func() bool {
		var dep appsv1.Deployment
		if c.Get(ctx, depNN, &dep) != nil {
			return false
		}
		return dep.Spec.Template.Spec.ServiceAccountName == "sa-edge"
	})
	var sa corev1.ServiceAccount
	if err := c.Get(ctx, types.NamespacedName{Name: "sa-edge", Namespace: "default"}, &sa); err != nil {
		t.Fatalf("agent SA must exist: %v", err)
	}
}

func TestConnectivityAutodiscoverObjectsAndSharedSubjects(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()
	mustCreate(t, c, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: nodeReader},
		Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"get", "list", "watch"}}}})
	mustCreate(t, c, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: nodeReader},
		RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: nodeReader}})
	t.Cleanup(func() {
		_ = c.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: nodeReader}})
		_ = c.Delete(ctx, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: nodeReader}})
	})
	mustCreate(t, c, &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "ad", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"ad.example"}}}) // #14(a): needs a hostname to issue an invite/token
	conn := towonelv1alpha1.ConnectivitySpec{Autodiscover: true, IrohPort: 5000, NodePort: towonelv1alpha1.NodePortSpec{Create: true}}
	mustCreate(t, c, &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "ad-a", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{TunnelRef: towonelv1alpha1.TunnelReference{Name: "ad"}, Connectivity: conn}})
	mustCreate(t, c, &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "ad-b", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{TunnelRef: towonelv1alpha1.TunnelReference{Name: "ad"}, Connectivity: conn}})
	waitFor(t, 20*time.Second, func() bool {
		var svc corev1.Service
		if c.Get(ctx, types.NamespacedName{Name: "ad-a-iroh", Namespace: "default"}, &svc) != nil {
			return false
		}
		return svc.Spec.Type == corev1.ServiceTypeNodePort &&
			svc.Spec.ExternalTrafficPolicy == corev1.ServiceExternalTrafficPolicyLocal &&
			len(svc.Spec.Ports) == 1 && svc.Spec.Ports[0].Protocol == corev1.ProtocolUDP
	})
	waitFor(t, 20*time.Second, func() bool {
		var crb rbacv1.ClusterRoleBinding
		if c.Get(ctx, types.NamespacedName{Name: nodeReader}, &crb) != nil {
			return false
		}
		return len(crb.Subjects) == 2
	})
}

func TestConnectivityDisablePrunes(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()
	installNodeReaderShell(t, c)
	mustCreate(t, c, &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "dp", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"dp.example"}}}) // #14(a): needs a hostname to issue an invite/token
	conn := towonelv1alpha1.ConnectivitySpec{Autodiscover: true, IrohPort: 5000, NodePort: towonelv1alpha1.NodePortSpec{Create: true}}
	ta := &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "dp-a", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{TunnelRef: towonelv1alpha1.TunnelReference{Name: "dp"}, Connectivity: conn}}
	mustCreate(t, c, ta)
	// Drop this agent at test end so it doesn't linger as a node-reader subject
	// for later tests (the binding aggregates all autodiscover agents suite-wide).
	t.Cleanup(func() { _ = c.Delete(ctx, ta) })
	// Service exists and the shared binding lists THIS agent's subject.
	waitFor(t, 20*time.Second, func() bool {
		var svc corev1.Service
		if c.Get(ctx, types.NamespacedName{Name: "dp-a-iroh", Namespace: "default"}, &svc) != nil {
			return false
		}
		return hasNodeReaderSubject(c, ctx, "dp-a", "default")
	})
	// Disable connectivity (conflict-retrying: the agent controller also writes status).
	agentNN := types.NamespacedName{Name: "dp-a", Namespace: "default"}
	waitFor(t, 15*time.Second, func() bool {
		var got towonelv1alpha1.TowonelAgent
		if c.Get(ctx, agentNN, &got) != nil {
			return false
		}
		got.Spec.Connectivity = towonelv1alpha1.ConnectivitySpec{}
		return c.Update(ctx, &got) == nil
	})
	// Service pruned and THIS agent's shared subject dropped.
	waitFor(t, 20*time.Second, func() bool {
		var svc corev1.Service
		if !apierrors.IsNotFound(c.Get(ctx, types.NamespacedName{Name: "dp-a-iroh", Namespace: "default"}, &svc)) {
			return false
		}
		return !hasNodeReaderSubject(c, ctx, "dp-a", "default")
	})
	// The per-agent ServiceAccount survives (it is always set, never pruned).
	var sa corev1.ServiceAccount
	if err := c.Get(ctx, types.NamespacedName{Name: "dp-a", Namespace: "default"}, &sa); err != nil {
		t.Fatalf("agent SA must survive connectivity disable: %v", err)
	}
}

func TestConnectivityInvalidStillReady(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()
	mustCreate(t, c, &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "inv", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"inv.example"}}}) // #14(a): needs a hostname to issue an invite/token
	mustCreate(t, c, &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "inv-a", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{TunnelRef: towonelv1alpha1.TunnelReference{Name: "inv"},
			Connectivity: towonelv1alpha1.ConnectivitySpec{Autodiscover: true, NodePort: towonelv1alpha1.NodePortSpec{Create: true}}}}) // no irohPort
	depNN := types.NamespacedName{Name: "inv-a", Namespace: "default"}
	waitFor(t, 20*time.Second, func() bool {
		var dep appsv1.Deployment
		return c.Get(ctx, depNN, &dep) == nil
	})
	markDeploymentAvailable(t, c, depNN)
	waitFor(t, 15*time.Second, func() bool {
		var got towonelv1alpha1.TowonelAgent
		if c.Get(ctx, depNN, &got) != nil {
			return false
		}
		return got.Status.Phase == "Ready" &&
			meta.IsStatusConditionPresentAndEqual(got.Status.Conditions, controller.CondIrohConnectivityReady, metav1.ConditionFalse)
	})
	var svc corev1.Service
	if err := c.Get(ctx, types.NamespacedName{Name: "inv-a-iroh", Namespace: "default"}, &svc); !apierrors.IsNotFound(err) {
		t.Fatalf("skipped autodiscover must not create a Service: %v", err)
	}
}

func TestConnectivityDeleteDropsSubject(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()
	installNodeReaderShell(t, c)
	mustCreate(t, c, &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "del", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"del.example"}}}) // #14(a): needs a hostname to issue an invite/token
	conn := towonelv1alpha1.ConnectivitySpec{Autodiscover: true, IrohPort: 5000, NodePort: towonelv1alpha1.NodePortSpec{Create: true}}
	ta := &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "del-a", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{TunnelRef: towonelv1alpha1.TunnelReference{Name: "del"}, Connectivity: conn}}
	mustCreate(t, c, ta)
	// This agent's SA subject lands in the shared binding.
	waitFor(t, 20*time.Second, func() bool {
		return hasNodeReaderSubject(c, ctx, "del-a", "default")
	})
	if err := c.Delete(ctx, ta); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	// Deleting the agent drops its subject (no finalizer; the NotFound reconcile
	// recomputes the shared binding's subjects).
	waitFor(t, 20*time.Second, func() bool {
		return !hasNodeReaderSubject(c, ctx, "del-a", "default")
	})
}

func TestConnectivityToggleRollsDeployment(t *testing.T) {
	t.Setenv("TOWONEL_API_KEY", "twk_env")
	c, _, stop := startManager(t)
	defer stop()
	ctx := t.Context()
	mustCreate(t, c, &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "tog", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"tog.example"}}}) // #14(a): needs a hostname to issue an invite/token
	mustCreate(t, c, &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "tog-a", Namespace: "default"},
		Spec: towonelv1alpha1.TowonelAgentSpec{TunnelRef: towonelv1alpha1.TunnelReference{Name: "tog"}}})
	depNN := types.NamespacedName{Name: "tog-a", Namespace: "default"}
	var hashBefore string
	waitFor(t, 20*time.Second, func() bool {
		var dep appsv1.Deployment
		if c.Get(ctx, depNN, &dep) != nil {
			return false
		}
		hashBefore = dep.Spec.Template.Annotations[controller.AnnotationConfigHash]
		return hashBefore != ""
	})
	// Set irohPort (conflict-retrying: the agent controller also writes status).
	agentNN := types.NamespacedName{Name: "tog-a", Namespace: "default"}
	waitFor(t, 15*time.Second, func() bool {
		var got towonelv1alpha1.TowonelAgent
		if c.Get(ctx, agentNN, &got) != nil {
			return false
		}
		got.Spec.Connectivity.IrohPort = 5000
		return c.Update(ctx, &got) == nil
	})
	// Connectivity env folds into the config-hash -> the pod template rolls.
	waitFor(t, 20*time.Second, func() bool {
		var dep appsv1.Deployment
		if c.Get(ctx, depNN, &dep) != nil {
			return false
		}
		return dep.Spec.Template.Annotations[controller.AnnotationConfigHash] != hashBefore
	})
}
