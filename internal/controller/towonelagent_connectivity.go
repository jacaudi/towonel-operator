package controller

import (
	"cmp"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

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
