package controller

import (
	"cmp"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// connectivityPlan is the resolved direct-path decision for one agent (design §4).
type connectivityPlan struct {
	irohPort        int32    // TOWONEL_AGENT_IROH_PORT + UDP containerPort (independent of autodiscover)
	extraLocalAddrs []string // TOWONEL_AGENT_EXTRA_LOCAL_ADDRS (independent)
	autodiscover    bool     // valid autodiscover path: env + Service + node-RBAC subject
	nodePortName    string   // resolved NodePort Service name (autodiscover only)
	nodePortPort    int32    // pinned external node port (0 = auto)
	skipped         bool     // an invalid combo was requested and skipped (non-wedging)
	skipReason      string   // message for the Event + IrohConnectivityReady=False
	portIgnored     bool     // NodePort.Port set while Create=false (informational)
}

// planConnectivity resolves spec.connectivity into a decision. autodiscover is
// the master switch for the managed-NodePort path: it requires nodePort.create
// AND irohPort>0, else it is skipped (Event + skip, never wedge — design §4).
func planConnectivity(ta *towonelv1alpha1.TowonelAgent) connectivityPlan {
	c := ta.Spec.Connectivity
	p := connectivityPlan{irohPort: c.IrohPort, extraLocalAddrs: c.ExtraLocalAddrs}
	switch {
	case c.Autodiscover && (!c.NodePort.Create || c.IrohPort == 0):
		p.skipped = true
		p.skipReason = "autodiscover requires nodePort.create=true and irohPort>0; skipping direct-path setup"
	case c.NodePort.Create && !c.Autodiscover:
		p.skipped = true
		p.skipReason = "nodePort.create requires autodiscover=true; skipping NodePort Service"
	case c.Autodiscover:
		p.autodiscover = true
		p.nodePortName = cmp.Or(c.NodePort.Name, ta.Name+"-iroh")
		p.nodePortPort = c.NodePort.Port
	}
	if c.NodePort.Port != 0 && !c.NodePort.Create {
		p.portIgnored = true
	}
	return p
}

// connectivityRequested reports whether the agent asked for ANY connectivity feature.
func connectivityRequested(ta *towonelv1alpha1.TowonelAgent) bool {
	c := ta.Spec.Connectivity
	return c.Autodiscover || c.IrohPort != 0 || len(c.ExtraLocalAddrs) > 0 || c.NodePort.Create
}

// connectivityEnv renders the connectivity env vars from the plan (design §4).
// Order is fixed for a stable hash + stable pod spec.
func connectivityEnv(ta *towonelv1alpha1.TowonelAgent, p connectivityPlan) []corev1.EnvVar {
	var env []corev1.EnvVar
	if p.irohPort != 0 {
		env = append(env, corev1.EnvVar{Name: "TOWONEL_AGENT_IROH_PORT", Value: strconv.Itoa(int(p.irohPort))})
	}
	if len(p.extraLocalAddrs) > 0 {
		env = append(env, corev1.EnvVar{Name: "TOWONEL_AGENT_EXTRA_LOCAL_ADDRS", Value: strings.Join(p.extraLocalAddrs, ",")})
	}
	if p.autodiscover {
		env = append(env,
			corev1.EnvVar{Name: "TOWONEL_AGENT_K8S_AUTODISCOVER", Value: "true"},
			corev1.EnvVar{Name: "TOWONEL_AGENT_K8S_SERVICE", Value: p.nodePortName},
			corev1.EnvVar{Name: "TOWONEL_AGENT_K8S_NAMESPACE", Value: ta.Namespace},
			corev1.EnvVar{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
			}},
		)
	}
	return env
}

// agentContainerPorts declares the agent's container ports: the metrics/health
// port (9090/TCP, served unconditionally — /metrics + /healthz + /readyz, #36)
// and the UDP iroh port when pinned (design §4/§7). The metrics port is always
// present so the chart PodMonitor (towonel.io agent metrics) can scrape it;
// declaring it is pure pod metadata (the agent already listens on 9090).
func agentContainerPorts(irohPort int32) []corev1.ContainerPort {
	ports := []corev1.ContainerPort{
		{Name: "metrics", ContainerPort: agentHealthPort, Protocol: corev1.ProtocolTCP},
	}
	if irohPort != 0 {
		ports = append(ports, corev1.ContainerPort{Name: "iroh", ContainerPort: irohPort, Protocol: corev1.ProtocolUDP})
	}
	return ports
}

func agentLabels(ta *towonelv1alpha1.TowonelAgent) map[string]string {
	return map[string]string{LabelAppName: AgentAppName, LabelAppInstance: ta.Name, LabelPartOf: PartOfValue}
}

func agentSelector(ta *towonelv1alpha1.TowonelAgent) map[string]string {
	return map[string]string{LabelAppName: AgentAppName, LabelAppInstance: ta.Name}
}

func buildServiceAccount(ta *towonelv1alpha1.TowonelAgent) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta: metav1.ObjectMeta{Name: agentSAName(ta.Name), Namespace: ta.Namespace, Labels: agentLabels(ta)},
	}
}

func buildNodePortService(ta *towonelv1alpha1.TowonelAgent, p connectivityPlan) *corev1.Service {
	return &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: metav1.ObjectMeta{Name: p.nodePortName, Namespace: ta.Namespace, Labels: agentLabels(ta)},
		Spec: corev1.ServiceSpec{
			Type:                  corev1.ServiceTypeNodePort,
			ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyLocal, // design §5.2 (I2)
			Selector:              agentSelector(ta),
			Ports: []corev1.ServicePort{{
				Name:       "iroh",
				Protocol:   corev1.ProtocolUDP,
				Port:       p.irohPort,
				TargetPort: intstr.FromInt32(p.irohPort),
				NodePort:   p.nodePortPort, // 0 = auto-assign
			}},
		},
	}
}

func buildServicesRBAC(ta *towonelv1alpha1.TowonelAgent) (*rbacv1.Role, *rbacv1.RoleBinding) {
	name := servicesReaderName(ta.Name)
	role := &rbacv1.Role{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ta.Namespace, Labels: agentLabels(ta)},
		Rules:      []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"services"}, Verbs: []string{"get", "list", "watch"}}},
	}
	rb := &rbacv1.RoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ta.Namespace, Labels: agentLabels(ta)},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: name},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: agentSAName(ta.Name), Namespace: ta.Namespace}},
	}
	return role, rb
}

// applyOwned SSA-applies an owned object (ownerRef -> agent), matching ensureDeployment.
func (r *TowonelAgentReconciler) applyOwned(ctx context.Context, ta *towonelv1alpha1.TowonelAgent, obj client.Object) error {
	if err := controllerutil.SetControllerReference(ta, obj, r.Scheme); err != nil {
		return fmt.Errorf("set owner ref on %T: %w", obj, err)
	}
	if err := r.Patch(ctx, obj, client.Apply, client.FieldOwner(FieldOwner), client.ForceOwnership); err != nil {
		return fmt.Errorf("apply %T %s/%s: %w", obj, obj.GetNamespace(), obj.GetName(), err)
	}
	return nil
}

// deleteOwnedIfExists prunes an agent-owned object by name (design §5.5): it
// deletes only when the live object is controller-owned by this agent, with an
// RV precondition, tolerating NotFound and Conflict (a concurrent change just
// re-reconciles). Never deletes a foreign object that happens to share the name.
func (r *TowonelAgentReconciler) deleteOwnedIfExists(ctx context.Context, ta *towonelv1alpha1.TowonelAgent, obj client.Object, nn types.NamespacedName) error {
	if err := r.Get(ctx, nn, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get %T %s for prune: %w", obj, nn, err)
	}
	if !metav1.IsControlledBy(obj, ta) {
		return nil // not ours — never steal/delete a foreign object
	}
	if err := r.Delete(ctx, obj, client.Preconditions{ResourceVersion: ptr.To(obj.GetResourceVersion())}); err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
		return fmt.Errorf("delete %T %s: %w", obj, nn, err)
	}
	return nil
}

// computeNodeReaderSubjects lists all agents and maps the valid-autodiscover
// ones to their SA subjects, sorted deterministically (design §5.3).
func (r *TowonelAgentReconciler) computeNodeReaderSubjects(ctx context.Context) ([]rbacv1.Subject, error) {
	var list towonelv1alpha1.TowonelAgentList
	if err := r.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("list agents for node-reader subjects: %w", err)
	}
	var subs []rbacv1.Subject
	for i := range list.Items {
		a := &list.Items[i]
		if !a.DeletionTimestamp.IsZero() {
			continue
		}
		if planConnectivity(a).autodiscover {
			subs = append(subs, rbacv1.Subject{Kind: "ServiceAccount", Name: agentSAName(a.Name), Namespace: a.Namespace})
		}
	}
	sort.Slice(subs, func(i, j int) bool {
		if subs[i].Namespace != subs[j].Namespace {
			return subs[i].Namespace < subs[j].Namespace
		}
		return subs[i].Name < subs[j].Name
	})
	return subs, nil
}

// reconcileNodeReaderSubjects RMW-patches the chart-owned shared ClusterRoleBinding's
// subjects to exactly the live autodiscover-agent SA set. Returns shellMissing=true
// (without error) when the chart shell is absent (design §5.3). No SSA: RMW avoids the
// Helm-3-way-merge field-manager tangle (the chart owns roleRef/labels; we own subjects).
func (r *TowonelAgentReconciler) reconcileNodeReaderSubjects(ctx context.Context) (shellMissing bool, err error) {
	desired, err := r.computeNodeReaderSubjects(ctx)
	if err != nil {
		return false, err
	}
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var crb rbacv1.ClusterRoleBinding
		if getErr := r.Get(ctx, types.NamespacedName{Name: nodeReaderName}, &crb); getErr != nil {
			if apierrors.IsNotFound(getErr) {
				shellMissing = true
				return nil
			}
			return getErr
		}
		if subjectsEqual(crb.Subjects, desired) {
			return nil
		}
		crb.Subjects = desired
		return r.Update(ctx, &crb)
	})
	if retryErr != nil {
		return false, fmt.Errorf("patch node-reader subjects: %w", retryErr)
	}
	return shellMissing, nil
}

func subjectsEqual(a, b []rbacv1.Subject) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ensureConnectivity applies the agent's direct-path objects (design §5/§9):
//   - the agent ServiceAccount ALWAYS (so the pod never runs as default);
//   - when autodiscover is valid: the UDP NodePort Service + services Role/RoleBinding;
//   - otherwise: prune any previously-created Service/Role/RoleBinding;
//   - always: recompute the shared node-reader binding subjects.
//
// Returns shellMissing for the IrohConnectivityReady projection. An invalid
// (skipped) plan is NOT an error.
func (r *TowonelAgentReconciler) ensureConnectivity(ctx context.Context, ta *towonelv1alpha1.TowonelAgent, p connectivityPlan) (shellMissing bool, err error) {
	if err := r.applyOwned(ctx, ta, buildServiceAccount(ta)); err != nil {
		return false, err
	}

	svcNN := types.NamespacedName{Namespace: ta.Namespace, Name: nodePortServiceName(ta)}
	roleNN := types.NamespacedName{Namespace: ta.Namespace, Name: servicesReaderName(ta.Name)}
	if p.autodiscover {
		if err := r.applyOwned(ctx, ta, buildNodePortService(ta, p)); err != nil {
			return false, err
		}
		role, rb := buildServicesRBAC(ta)
		if err := r.applyOwned(ctx, ta, role); err != nil {
			return false, err
		}
		if err := r.applyOwned(ctx, ta, rb); err != nil {
			return false, err
		}
	} else {
		// prune-after (design §5.5): only delete what we may have created.
		//
		// Edge: svcNN is derived from nodePortServiceName(ta), which reads
		// ta.Spec.Connectivity.NodePort.Name (defaulting to "<agent>-iroh").
		// If the agent was previously created with a custom NodePort.Name and
		// the entire connectivity block is then cleared, svcNN resolves to the
		// default name and the custom-named Service is missed here — it lingers
		// until the agent is deleted, at which point ownerRef GC reclaims it.
		// This is intentional: the GC backstop is an acceptable safety net and
		// a label-sweep would add complexity without meaningfully shortening the
		// window (KISS).
		if err := r.deleteOwnedIfExists(ctx, ta, &corev1.Service{}, svcNN); err != nil {
			return false, err
		}
		if err := r.deleteOwnedIfExists(ctx, ta, &rbacv1.RoleBinding{}, roleNN); err != nil {
			return false, err
		}
		if err := r.deleteOwnedIfExists(ctx, ta, &rbacv1.Role{}, roleNN); err != nil {
			return false, err
		}
	}

	return r.reconcileNodeReaderSubjects(ctx)
}
