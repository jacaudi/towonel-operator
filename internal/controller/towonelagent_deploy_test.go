package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func renderAgent() *towonelv1alpha1.TowonelAgent {
	return &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-a", Namespace: "selfhosted"},
		Spec: towonelv1alpha1.TowonelAgentSpec{
			TunnelRef: towonelv1alpha1.TunnelReference{Name: "app", Namespace: "network"},
			Services: []towonelv1alpha1.AgentService{
				{Hostname: "app.example", Origin: "app:8080", EdgeTLSMode: "passthrough"},
			},
			TCP: []towonelv1alpha1.AgentL4Service{
				{Name: "ssh", Origin: "app:22"},
				{Name: "pending", Origin: "app:9"},
			},
		},
	}
}

func allocsFor() []towonelv1alpha1.PortAllocation {
	return []towonelv1alpha1.PortAllocation{{Name: "ssh", Protocol: "tcp", ListenPort: 2222}}
}

func TestRenderConfigPartialRender(t *testing.T) {
	cfg, err := renderConfig(renderAgent(), allocsFor(), "inv-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.ServicesJSON, `"hostname":"app.example"`) || !strings.Contains(cfg.ServicesJSON, `"tls_mode":{"mode":"passthrough"}`) {
		t.Errorf("services json = %s", cfg.ServicesJSON)
	}
	if !strings.Contains(cfg.TCPJSON, `"listen_port":2222`) {
		t.Errorf("tcp json = %s", cfg.TCPJSON)
	}
	if strings.Contains(cfg.TCPJSON, "pending") {
		t.Errorf("unallocated entry must be omitted: %s", cfg.TCPJSON)
	}
	if cfg.UDPJSON != "" {
		t.Errorf("empty udp must render as empty string, got %s", cfg.UDPJSON)
	}
	if len(cfg.Pending) != 1 || cfg.Pending[0] != "tcp/pending" {
		t.Errorf("pending = %v", cfg.Pending)
	}
}

func TestConfigHashSemantics(t *testing.T) {
	ta := renderAgent()
	a, _ := renderConfig(ta, allocsFor(), "inv-1")
	b, _ := renderConfig(ta, allocsFor(), "inv-1")
	if a.hash() != b.hash() {
		t.Error("hash must be stable across renders")
	}
	c, _ := renderConfig(ta, allocsFor(), "inv-2") // rotation -> new invite id
	if a.hash() == c.hash() {
		t.Error("hash must change on invite-id change")
	}
	ta2 := renderAgent()
	ta2.Spec.Services[0].Hostname = "other.example"
	d, _ := renderConfig(ta2, allocsFor(), "inv-1")
	if a.hash() == d.hash() {
		t.Error("hash must change on services change")
	}
}

func TestBuildDeployment(t *testing.T) {
	ta := renderAgent()
	cfg, _ := renderConfig(ta, allocsFor(), "inv-1")
	dep := buildDeployment(ta, cfg)
	if dep.Name != "edge-a" || dep.Namespace != "selfhosted" {
		t.Fatalf("name/ns = %s/%s", dep.Namespace, dep.Name)
	}
	if *dep.Spec.Replicas != 1 {
		t.Errorf("default replicas = %d", *dep.Spec.Replicas)
	}
	pod := dep.Spec.Template
	if pod.Annotations[AnnotationConfigHash] != cfg.hash() {
		t.Error("pod template must carry the config hash")
	}
	ctr := pod.Spec.Containers[0]
	if ctr.Image != defaultAgentImage {
		t.Errorf("image = %s", ctr.Image)
	}
	envByName := map[string]corev1.EnvVar{}
	for _, e := range ctr.Env {
		envByName[e.Name] = e
	}
	if envByName["TOWONEL_INVITE_TOKEN"].ValueFrom.SecretKeyRef.Name != "edge-a-token" {
		t.Error("token env must come from the agent secret")
	}
	if envByName["TOWONEL_AGENT_SERVICES"].Value != cfg.ServicesJSON {
		t.Error("services env mismatch")
	}
	if _, ok := envByName["TOWONEL_AGENT_UDP_SERVICES"]; ok {
		t.Error("empty udp list must not render an env var")
	}
	if envByName["TOWONEL_AGENT_HEALTH_LISTEN_ADDR"].Value != agentHealthAddr {
		t.Error("health listen addr")
	}
	if ctr.ReadinessProbe == nil || ctr.ReadinessProbe.HTTPGet.Path != "/readyz" {
		t.Error("readiness probe must gate on /readyz (issue #42)")
	}
	if ctr.LivenessProbe == nil || ctr.LivenessProbe.HTTPGet.Path != "/healthz" {
		t.Error("liveness probe must stay on /healthz")
	}
	if ctr.Resources.Requests.Memory().String() != "128Mi" || ctr.Resources.Limits.Memory().String() != "512Mi" {
		t.Errorf("OOM-safe defaults: %+v", ctr.Resources)
	}
	if dep.Spec.Selector.MatchLabels[LabelAppInstance] != "edge-a" {
		t.Error("selector instance label")
	}
	// #36: the agent container always declares the metrics port so the chart
	// PodMonitor can scrape /metrics on 9090.
	var hasMetrics bool
	for _, p := range ctr.Ports {
		if p.Name == "metrics" && p.ContainerPort == agentHealthPort && p.Protocol == corev1.ProtocolTCP {
			hasMetrics = true
		}
	}
	if !hasMetrics {
		t.Errorf("agent container must declare the metrics port (9090/TCP); got %+v", ctr.Ports)
	}
	// #36: pod-template labels the chart PodMonitor selects on. Regression guard —
	// the selector silently depends on these (set by an inline map in buildDeployment,
	// not agentLabels), so a divergence here would break scraping.
	if pod.Labels[LabelAppName] != AgentAppName || pod.Labels[LabelPartOf] != PartOfValue {
		t.Errorf("pod template must carry %s=%s + %s=%s for the PodMonitor selector; got %v",
			LabelAppName, AgentAppName, LabelPartOf, PartOfValue, pod.Labels)
	}
}

func TestBuildDeploymentPartialResources(t *testing.T) {
	ta := renderAgent()
	ta.Spec.Workload.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
	}
	cfg, _ := renderConfig(ta, allocsFor(), "inv-1")
	dep := buildDeployment(ta, cfg)
	res := dep.Spec.Template.Spec.Containers[0].Resources
	if res.Requests.Memory().String() != "64Mi" {
		t.Errorf("user memory request must be kept: %s", res.Requests.Memory())
	}
	if res.Limits.Memory().String() != "512Mi" {
		t.Errorf("memory limit must default independently: %s", res.Limits.Memory())
	}
	// Defaulting must never mutate the cached spec object.
	if _, ok := ta.Spec.Workload.Resources.Limits[corev1.ResourceMemory]; ok {
		t.Error("buildDeployment mutated ta.Spec.Workload.Resources")
	}
}

func TestDeploymentNeedsWrite(t *testing.T) {
	ta := renderAgent()
	cfg, _ := renderConfig(ta, allocsFor(), "inv-1")
	desired := buildDeployment(ta, cfg)
	if deploymentNeedsWrite(desired.DeepCopy(), desired) {
		t.Error("identical deployment must not need a write")
	}
	changed := desired.DeepCopy()
	changed.Spec.Template.Annotations[AnnotationConfigHash] = "stale"
	if !deploymentNeedsWrite(changed, desired) {
		t.Error("hash change must need a write")
	}
	scaled := desired.DeepCopy()
	scaled.Spec.Replicas = new(int32(5)) // outside the hash -> still a write
	if !deploymentNeedsWrite(scaled, desired) {
		t.Error("replica change must need a write")
	}
}

func TestBuildDeploymentSecurityContextDefaults(t *testing.T) {
	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-a", Namespace: "ns"},
	}
	dep := buildDeployment(ta, agentConfig{Image: "img", SAName: "agent-a"})

	pod := dep.Spec.Template.Spec.SecurityContext
	if pod == nil || pod.RunAsNonRoot == nil || !*pod.RunAsNonRoot {
		t.Fatalf("pod securityContext default missing/incorrect: %+v", pod)
	}
	if pod.RunAsUser == nil || *pod.RunAsUser != 10001 || pod.FSGroup == nil || *pod.FSGroup != 10001 {
		t.Fatalf("pod uid/fsGroup default = %+v, want 10001", pod)
	}
	if pod.SeccompProfile == nil || pod.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("pod seccompProfile default = %+v", pod.SeccompProfile)
	}

	c := dep.Spec.Template.Spec.Containers[0].SecurityContext
	if c == nil || c.AllowPrivilegeEscalation == nil || *c.AllowPrivilegeEscalation {
		t.Fatalf("container allowPrivilegeEscalation default = %+v, want false", c)
	}
	if c.ReadOnlyRootFilesystem == nil || !*c.ReadOnlyRootFilesystem {
		t.Fatalf("container readOnlyRootFilesystem default = %+v, want true", c)
	}
	if c.Capabilities == nil || len(c.Capabilities.Drop) != 1 || c.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("container capabilities.drop default = %+v, want [ALL]", c.Capabilities)
	}
}

func TestBuildDeploymentSecurityContextUserOverrideWins(t *testing.T) {
	userPod := &corev1.PodSecurityContext{RunAsUser: ptr.To(int64(2000))}
	userCtr := &corev1.SecurityContext{ReadOnlyRootFilesystem: ptr.To(false)}
	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-a", Namespace: "ns"},
		Spec: towonelv1alpha1.TowonelAgentSpec{Workload: towonelv1alpha1.WorkloadSpec{
			PodSecurityContext: userPod, SecurityContext: userCtr,
		}},
	}
	dep := buildDeployment(ta, agentConfig{Image: "img", SAName: "agent-a"})

	pod := dep.Spec.Template.Spec.SecurityContext
	if pod.RunAsUser == nil || *pod.RunAsUser != 2000 || pod.RunAsNonRoot != nil {
		t.Fatalf("user pod securityContext not honored wholesale: %+v", pod)
	}
	c := dep.Spec.Template.Spec.Containers[0].SecurityContext
	if c.ReadOnlyRootFilesystem == nil || *c.ReadOnlyRootFilesystem {
		t.Fatalf("user container securityContext not honored wholesale: %+v", c)
	}
}

func TestDeploymentNeedsWriteSecurityContextChange(t *testing.T) {
	ta := &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "agent-a", Namespace: "ns"}}
	cur := buildDeployment(ta, agentConfig{Image: "img", SAName: "agent-a"})

	ta2 := ta.DeepCopy()
	ta2.Spec.Workload.SecurityContext = &corev1.SecurityContext{ReadOnlyRootFilesystem: ptr.To(false)}
	desired := buildDeployment(ta2, agentConfig{Image: "img", SAName: "agent-a"})
	if !deploymentNeedsWrite(cur, desired) {
		t.Fatal("container securityContext change must trigger a write")
	}

	ta3 := ta.DeepCopy()
	ta3.Spec.Workload.PodSecurityContext = &corev1.PodSecurityContext{RunAsUser: ptr.To(int64(2000))}
	desired3 := buildDeployment(ta3, agentConfig{Image: "img", SAName: "agent-a"})
	if !deploymentNeedsWrite(cur, desired3) {
		t.Fatal("pod securityContext change must trigger a write")
	}
}
