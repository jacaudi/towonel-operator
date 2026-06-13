package controller

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
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

func TestConnectivityEnvAutodiscover(t *testing.T) {
	ta := agentWithConn(towonelv1alpha1.ConnectivitySpec{
		Autodiscover: true, IrohPort: 5000, ExtraLocalAddrs: []string{"1.2.3.4:5000", "[::1]:5000"},
		NodePort: towonelv1alpha1.NodePortSpec{Create: true},
	})
	env := connectivityEnv(ta, planConnectivity(ta))
	got := map[string]corev1.EnvVar{}
	for _, e := range env {
		got[e.Name] = e
	}
	if got["TOWONEL_AGENT_IROH_PORT"].Value != "5000" {
		t.Errorf("iroh port env = %q", got["TOWONEL_AGENT_IROH_PORT"].Value)
	}
	if got["TOWONEL_AGENT_EXTRA_LOCAL_ADDRS"].Value != "1.2.3.4:5000,[::1]:5000" {
		t.Errorf("extra addrs env = %q", got["TOWONEL_AGENT_EXTRA_LOCAL_ADDRS"].Value)
	}
	if got["TOWONEL_AGENT_K8S_AUTODISCOVER"].Value != "true" {
		t.Errorf("autodiscover env missing")
	}
	if got["TOWONEL_AGENT_K8S_SERVICE"].Value != "edge-a-iroh" {
		t.Errorf("k8s service env = %q", got["TOWONEL_AGENT_K8S_SERVICE"].Value)
	}
	if got["TOWONEL_AGENT_K8S_NAMESPACE"].Value != "selfhosted" {
		t.Errorf("k8s namespace env = %q", got["TOWONEL_AGENT_K8S_NAMESPACE"].Value)
	}
	nn := got["NODE_NAME"]
	if nn.ValueFrom == nil || nn.ValueFrom.FieldRef == nil || nn.ValueFrom.FieldRef.FieldPath != "spec.nodeName" {
		t.Errorf("NODE_NAME must be downward-API spec.nodeName, got %+v", nn)
	}
}

func TestConnectivityEnvSkippedHasNoAutodiscover(t *testing.T) {
	ta := agentWithConn(towonelv1alpha1.ConnectivitySpec{Autodiscover: true, NodePort: towonelv1alpha1.NodePortSpec{Create: true}}) // no irohPort -> skipped
	for _, e := range connectivityEnv(ta, planConnectivity(ta)) {
		if e.Name == "TOWONEL_AGENT_K8S_AUTODISCOVER" {
			t.Error("skipped autodiscover must not render autodiscover env")
		}
	}
}

func TestHashChangesWhenConnectivityChanges(t *testing.T) {
	base := renderAgent()
	c0, _ := renderConfig(base, nil, "inv-1")
	withConn := renderAgent()
	withConn.Spec.Connectivity = towonelv1alpha1.ConnectivitySpec{IrohPort: 5000}
	c1, _ := renderConfig(withConn, nil, "inv-1")
	if c0.hash() == c1.hash() {
		t.Error("hash must change when connectivity env changes")
	}
}

func TestHashStableAcrossNodePortValue(t *testing.T) {
	a := renderAgent()
	a.Spec.Connectivity = towonelv1alpha1.ConnectivitySpec{Autodiscover: true, IrohPort: 5000, NodePort: towonelv1alpha1.NodePortSpec{Create: true}}
	c1, _ := renderConfig(a, nil, "inv-1")
	c2, _ := renderConfig(a, nil, "inv-1")
	if c1.hash() != c2.hash() {
		t.Error("hash must be deterministic / independent of the runtime nodePort")
	}
}

func TestBuildNodePortService(t *testing.T) {
	ta := agentWithConn(towonelv1alpha1.ConnectivitySpec{Autodiscover: true, IrohPort: 5000, NodePort: towonelv1alpha1.NodePortSpec{Create: true, Port: 31000}})
	svc := buildNodePortService(ta, planConnectivity(ta))
	if svc.Name != "edge-a-iroh" || svc.Namespace != "selfhosted" {
		t.Fatalf("name/ns = %s/%s", svc.Name, svc.Namespace)
	}
	if svc.Spec.Type != corev1.ServiceTypeNodePort {
		t.Errorf("type = %s", svc.Spec.Type)
	}
	if svc.Spec.ExternalTrafficPolicy != corev1.ServiceExternalTrafficPolicyLocal {
		t.Errorf("externalTrafficPolicy = %s want Local", svc.Spec.ExternalTrafficPolicy)
	}
	if svc.Spec.Selector[LabelAppInstance] != "edge-a" || svc.Spec.Selector[LabelAppName] != AgentAppName {
		t.Errorf("selector = %v", svc.Spec.Selector)
	}
	p := svc.Spec.Ports[0]
	if p.Protocol != corev1.ProtocolUDP || p.Port != 5000 || p.TargetPort.IntValue() != 5000 || p.NodePort != 31000 {
		t.Errorf("port spec = %+v", p)
	}
}

func TestBuildNodePortServiceAutoPort(t *testing.T) {
	ta := agentWithConn(towonelv1alpha1.ConnectivitySpec{Autodiscover: true, IrohPort: 5000, NodePort: towonelv1alpha1.NodePortSpec{Create: true}})
	svc := buildNodePortService(ta, planConnectivity(ta))
	if svc.Spec.Ports[0].NodePort != 0 {
		t.Errorf("unpinned nodePort must be 0 (auto-assign), got %d", svc.Spec.Ports[0].NodePort)
	}
}

func TestBuildServicesRBAC(t *testing.T) {
	ta := agentWithConn(towonelv1alpha1.ConnectivitySpec{Autodiscover: true, IrohPort: 5000, NodePort: towonelv1alpha1.NodePortSpec{Create: true}})
	role, rb := buildServicesRBAC(ta)
	if role.Name != "edge-a-iroh-svc-reader" || len(role.Rules) != 1 {
		t.Fatalf("role = %+v", role)
	}
	if role.Rules[0].Resources[0] != "services" || !slices.Contains(role.Rules[0].Verbs, "get") {
		t.Errorf("role rule = %+v", role.Rules[0])
	}
	if rb.RoleRef.Name != role.Name || rb.Subjects[0].Name != agentSAName(ta.Name) || rb.Subjects[0].Namespace != ta.Namespace {
		t.Errorf("rolebinding = %+v", rb)
	}
}
