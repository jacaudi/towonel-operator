package controller

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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

// agentContainerPorts declares the UDP iroh port when pinned (design §4/§7).
func agentContainerPorts(irohPort int32) []corev1.ContainerPort {
	if irohPort == 0 {
		return nil
	}
	return []corev1.ContainerPort{{Name: "iroh", ContainerPort: irohPort, Protocol: corev1.ProtocolUDP}}
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

// deleteOwnedIfExists deletes an owned object by name, tolerating NotFound (prune-after).
func (r *TowonelAgentReconciler) deleteOwnedIfExists(ctx context.Context, obj client.Object, nn types.NamespacedName) error {
	obj.SetName(nn.Name)
	obj.SetNamespace(nn.Namespace)
	if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete %T %s: %w", obj, nn, err)
	}
	return nil
}
