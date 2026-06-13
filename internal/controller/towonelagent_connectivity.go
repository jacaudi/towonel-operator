package controller

import (
	"cmp"

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
