package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func agentWithConn(c towonelv1alpha1.ConnectivitySpec) *towonelv1alpha1.TowonelAgent {
	return &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-a", Namespace: "selfhosted"},
		Spec:       towonelv1alpha1.TowonelAgentSpec{Connectivity: c},
	}
}

func TestPlanConnectivityMatrix(t *testing.T) {
	tests := []struct {
		name                      string
		conn                      towonelv1alpha1.ConnectivitySpec
		wantAutodiscover, skipped bool
		wantIroh                  int32
		wantSvc                   string
	}{
		{"empty", towonelv1alpha1.ConnectivitySpec{}, false, false, 0, ""},
		{"extraLocalAddrs only", towonelv1alpha1.ConnectivitySpec{ExtraLocalAddrs: []string{"1.2.3.4:5000"}}, false, false, 0, ""},
		{"irohPort only", towonelv1alpha1.ConnectivitySpec{IrohPort: 5000}, false, false, 5000, ""},
		{"valid autodiscover", towonelv1alpha1.ConnectivitySpec{Autodiscover: true, IrohPort: 5000, NodePort: towonelv1alpha1.NodePortSpec{Create: true}}, true, false, 5000, "edge-a-iroh"},
		{"autodiscover no irohPort", towonelv1alpha1.ConnectivitySpec{Autodiscover: true, NodePort: towonelv1alpha1.NodePortSpec{Create: true}}, false, true, 0, ""},
		{"autodiscover no create", towonelv1alpha1.ConnectivitySpec{Autodiscover: true, IrohPort: 5000}, false, true, 5000, ""},
		{"create without autodiscover", towonelv1alpha1.ConnectivitySpec{IrohPort: 5000, NodePort: towonelv1alpha1.NodePortSpec{Create: true}}, false, true, 5000, ""},
		{"name override", towonelv1alpha1.ConnectivitySpec{Autodiscover: true, IrohPort: 5000, NodePort: towonelv1alpha1.NodePortSpec{Create: true, Name: "custom"}}, true, false, 5000, "custom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := planConnectivity(agentWithConn(tc.conn))
			if p.autodiscover != tc.wantAutodiscover {
				t.Errorf("autodiscover=%v want %v", p.autodiscover, tc.wantAutodiscover)
			}
			if p.skipped != tc.skipped {
				t.Errorf("skipped=%v want %v (reason %q)", p.skipped, tc.skipped, p.skipReason)
			}
			if p.irohPort != tc.wantIroh {
				t.Errorf("irohPort=%d want %d", p.irohPort, tc.wantIroh)
			}
			if p.autodiscover && p.nodePortName != tc.wantSvc {
				t.Errorf("nodePortName=%q want %q", p.nodePortName, tc.wantSvc)
			}
		})
	}
}

func TestPlanConnectivityPortIgnored(t *testing.T) {
	p := planConnectivity(agentWithConn(towonelv1alpha1.ConnectivitySpec{
		IrohPort: 5000, NodePort: towonelv1alpha1.NodePortSpec{Port: 31000}, // create=false
	}))
	if !p.portIgnored {
		t.Error("expected portIgnored when Port set without Create")
	}
}
