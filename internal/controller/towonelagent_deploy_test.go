package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
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

func TestBuildDeploymentAffinity(t *testing.T) {
	affinity := &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key: "kubernetes.io/arch", Operator: corev1.NodeSelectorOpIn, Values: []string{"amd64"},
					}},
				}},
			},
		},
		PodAffinity: &corev1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "cache"}},
				TopologyKey:   "kubernetes.io/hostname",
			}},
		},
		PodAntiAffinity: &corev1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{LabelAppInstance: "edge-a"}},
				TopologyKey:   "kubernetes.io/hostname",
			}},
		},
	}
	ta := renderAgent()
	ta.Spec.Workload.Affinity = affinity
	want := affinity.DeepCopy()
	cfg, _ := renderConfig(ta, allocsFor(), "inv-1")
	dep := buildDeployment(ta, cfg)
	if !equality.Semantic.DeepEqual(dep.Spec.Template.Spec.Affinity, want) {
		t.Errorf("pod affinity = %+v, want %+v", dep.Spec.Template.Spec.Affinity, affinity)
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

func TestDeploymentNeedsWriteAffinityChange(t *testing.T) {
	ta := &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "agent-a", Namespace: "ns"}}
	base := buildDeployment(ta, agentConfig{Image: "img", SAName: "agent-a"})
	if deploymentNeedsWrite(base.DeepCopy(), base) {
		t.Fatal("both nil affinities must not need a write")
	}

	withAffinity := base.DeepCopy()
	withAffinity.Spec.Template.Spec.Affinity = &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				TopologyKey: "kubernetes.io/hostname",
			}},
		},
	}
	if !deploymentNeedsWrite(base, withAffinity) {
		t.Fatal("nil to set affinity change must trigger a write")
	}
	if deploymentNeedsWrite(withAffinity.DeepCopy(), withAffinity) {
		t.Fatal("identical affinity must not need a write")
	}

	modified := withAffinity.DeepCopy()
	modified.Spec.Template.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].TopologyKey = "topology.kubernetes.io/zone"
	if !deploymentNeedsWrite(withAffinity, modified) {
		t.Fatal("modified affinity must trigger a write")
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

func TestDisableUDPGSO(t *testing.T) {
	base := renderAgent()
	baseCfg, err := renderConfig(base, allocsFor(), "inv-1")
	if err != nil {
		t.Fatal(err)
	}
	original := buildDeployment(base, baseCfg)
	const envName = "TOWONEL_DISABLE_UDP_GSO"
	for _, env := range original.Spec.Template.Spec.Containers[0].Env {
		if env.Name == envName {
			t.Fatal("omitted setting must preserve the agent default")
		}
	}

	enabled := base.DeepCopy()
	enabled.Spec.Workload.DisableUDPGSO = true
	enabledCfg, err := renderConfig(enabled, allocsFor(), "inv-1")
	if err != nil {
		t.Fatal(err)
	}
	desired := buildDeployment(enabled, enabledCfg)
	count := 0
	for _, env := range desired.Spec.Template.Spec.Containers[0].Env {
		if env.Name == envName {
			count++
			if env.Value != "true" || env.ValueFrom != nil {
				t.Fatalf("unexpected GSO override: %+v", env)
			}
		}
	}
	if count != 1 {
		t.Fatalf("GSO override count = %d, want 1", count)
	}
	if baseCfg.hash() == enabledCfg.hash() {
		t.Fatal("enabling the setting must change the rollout hash")
	}
	if !deploymentNeedsWrite(original, desired) {
		t.Fatal("enabling the setting must trigger reconciliation")
	}

	enabled.Spec.Workload.DisableUDPGSO = false
	disabledCfg, err := renderConfig(enabled, allocsFor(), "inv-1")
	if err != nil {
		t.Fatal(err)
	}
	restored := buildDeployment(enabled, disabledCfg)
	if disabledCfg.hash() != baseCfg.hash() || deploymentNeedsWrite(original, restored) {
		t.Fatal("false must restore the original Deployment and hash")
	}
	if !deploymentNeedsWrite(desired, restored) {
		t.Fatal("disabling the setting must trigger reconciliation")
	}
}

func TestConfigHashMatchesPreviousRelease(t *testing.T) {
	// Computed on main before disableUDPGSO; changing it re-rolls every existing agent, so update only for a deliberate hash-contract change.
	const want = "a14a6002cbdd6c7045bc5c4846f6b9e4c942120d6cd05fe70e169b2f689375b4"
	ta := renderAgent()
	ta.Spec.Workload.Image = "example.test/towonel-agent:pinned" // decouple from defaultAgentImage bumps
	cfg, err := renderConfig(ta, allocsFor(), "inv-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.hash(); got != want {
		t.Errorf("config hash = %s, want %s", got, want)
	}
}

func TestParseAgentEnv(t *testing.T) {
	got, err := ParseAgentEnv([]string{
		"RUST_LOG=info,iroh::socket::transports=trace", // value keeps its own '=' and ','
		"EMPTY=",
		"A_FIRST=1",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []corev1.EnvVar{ // sorted by name: flag order must not change the rollout hash
		{Name: "A_FIRST", Value: "1"},
		{Name: "EMPTY", Value: ""},
		{Name: "RUST_LOG", Value: "info,iroh::socket::transports=trace"},
	}
	if !equality.Semantic.DeepEqual(got, want) {
		t.Fatalf("ParseAgentEnv = %+v, want %+v", got, want)
	}
	if got, err := ParseAgentEnv(nil); err != nil || got != nil {
		t.Fatalf("no entries must yield nil, nil; got %+v, %v", got, err)
	}
	for name, in := range map[string][]string{
		"missing '='":    {"RUST_LOG"},
		"empty name":     {"=value"},
		"invalid name":   {"BAD NAME=1"},
		"duplicate name": {"RUST_LOG=a", "RUST_LOG=b"},
	} {
		if _, err := ParseAgentEnv(in); err == nil {
			t.Errorf("%s: %q must be rejected so a misconfigured manager never starts", name, in)
		}
	}
}

func TestAgentExtraEnv(t *testing.T) {
	ta := renderAgent()
	baseCfg, err := renderConfig(ta, allocsFor(), "inv-1")
	if err != nil {
		t.Fatal(err)
	}
	original := buildDeployment(ta, baseCfg)

	extra := []corev1.EnvVar{{Name: "A_FIRST", Value: "1"}, {Name: "RUST_LOG", Value: "debug"}}
	cfg, err := baseCfg.withExtraEnv(ta, extra)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.IgnoredEnv) != 0 {
		t.Fatalf("no entry collides with an operator-managed name, got ignored %v", cfg.IgnoredEnv)
	}
	desired := buildDeployment(ta, cfg)
	env := desired.Spec.Template.Spec.Containers[0].Env
	// Extra entries follow every operator-managed one.
	if got := env[len(env)-2:]; !equality.Semantic.DeepEqual(got, extra) {
		t.Fatalf("extra env not appended verbatim: %+v", got)
	}
	if baseCfg.hash() == cfg.hash() {
		t.Fatal("adding agent env must change the rollout hash")
	}
	if !deploymentNeedsWrite(original, desired) {
		t.Fatal("adding agent env must trigger reconciliation")
	}

	changed, err := baseCfg.withExtraEnv(ta, []corev1.EnvVar{{Name: "A_FIRST", Value: "1"}, {Name: "RUST_LOG", Value: "trace"}})
	if err != nil {
		t.Fatal(err)
	}
	if changed.hash() == cfg.hash() {
		t.Fatal("changing an agent env value must change the rollout hash")
	}

	// No operator-wide env: existing agents keep their hash across the upgrade.
	none, err := baseCfg.withExtraEnv(ta, nil)
	if err != nil {
		t.Fatal(err)
	}
	if none.hash() != baseCfg.hash() {
		t.Fatal("no agent env must leave the rollout hash unchanged")
	}
}

// The Deployment is server-side applied and containers[].env is a name-keyed
// list, so a duplicate name fails the apply outright. Operator-managed names
// therefore win and the colliding entry is dropped and reported.
func TestAgentExtraEnvCannotOverrideOperatorManaged(t *testing.T) {
	ta := renderAgent()
	ta.Spec.RelayURL = "https://relay.example"
	base, err := renderConfig(ta, allocsFor(), "inv-1")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := base.withExtraEnv(ta, []corev1.EnvVar{
		{Name: "RUST_LOG", Value: "debug"},
		{Name: "TOWONEL_AGENT_HEALTH_LISTEN_ADDR", Value: "0.0.0.0:1"},
		{Name: "TOWONEL_AGENT_RELAY_URL", Value: "https://other.example"},
		{Name: "TOWONEL_INVITE_TOKEN", Value: "hijack"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantIgnored := []string{"TOWONEL_AGENT_HEALTH_LISTEN_ADDR", "TOWONEL_AGENT_RELAY_URL", "TOWONEL_INVITE_TOKEN"}
	if !equality.Semantic.DeepEqual(cfg.IgnoredEnv, wantIgnored) {
		t.Fatalf("IgnoredEnv = %v, want %v", cfg.IgnoredEnv, wantIgnored)
	}
	seen := map[string]corev1.EnvVar{}
	for _, e := range buildDeployment(ta, cfg).Spec.Template.Spec.Containers[0].Env {
		if _, dup := seen[e.Name]; dup {
			t.Fatalf("duplicate env name %q would fail the server-side apply", e.Name)
		}
		seen[e.Name] = e
	}
	if e := seen["TOWONEL_INVITE_TOKEN"]; e.Value != "" || e.ValueFrom == nil {
		t.Fatalf("invite token must stay the operator's secret ref, got %+v", e)
	}
	if got := seen["TOWONEL_AGENT_RELAY_URL"].Value; got != "https://relay.example" {
		t.Fatalf("relay URL = %q, want the spec.relayURL value", got)
	}
	if got := seen["TOWONEL_AGENT_HEALTH_LISTEN_ADDR"].Value; got != agentHealthAddr {
		t.Fatalf("health addr = %q, want %q (probes depend on it)", got, agentHealthAddr)
	}
	if got := seen["RUST_LOG"].Value; got != "debug" {
		t.Fatalf("non-colliding entry must survive, RUST_LOG = %q", got)
	}

	// Dropped entries are not part of the rendered pod, so they must not roll it.
	only, err := base.withExtraEnv(ta, []corev1.EnvVar{{Name: "RUST_LOG", Value: "debug"}})
	if err != nil {
		t.Fatal(err)
	}
	if only.hash() != cfg.hash() {
		t.Fatal("ignored entries must not affect the rollout hash")
	}
}
