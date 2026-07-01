package controller

import (
	"os/exec"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// Issue #43: the agent PodMonitor only scrapes if its selector labels match the
// labels the operator stamps on agent pods. Those two facts live on opposite
// sides of the Go/Helm boundary and were an UNGUARDED DRY pair:
//
//   - Go side: pod-template labels come from AgentAppName / PartOfValue
//     (conventions.go), applied in buildDeployment (towonelagent_deploy.go).
//   - Chart side: chart/templates/agent-podmonitor.yaml hardcodes the selector
//     literals app.kubernetes.io/name + app.kubernetes.io/part-of.
//
// Before this test a one-sided rename kept BOTH existing tests green
// (TestBuildDeployment asserts pod labels against the same Go constants;
// taskfile helm-template-test greps the chart literals against its own hardcoded
// copy) while silently breaking metrics scraping. This test closes the gap
// (issue option 1): it renders the chart with `helm template` and asserts the
// rendered PodMonitor selector equals the GO CONSTANTS — not a third hardcoded
// copy — so a change on EITHER side without the other fails here.

// podMonitorSelector is the minimal shape we decode from a rendered PodMonitor.
type podMonitorSelector struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Selector struct {
			MatchLabels map[string]string `json:"matchLabels"`
		} `json:"selector"`
	} `json:"spec"`
}

// renderAgentPodMonitor shells out to `helm template` with the agent PodMonitor
// enabled and returns the decoded PodMonitor selector document. It skips (not
// fails) when the helm binary is unavailable so `go test ./...` stays green in
// sandboxes without helm; the maintainer's CI has helm (it runs helm-lint /
// helm-template-test), so the guard executes there.
func renderAgentPodMonitor(t *testing.T) podMonitorSelector {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm binary not found on PATH; cross-boundary render guard runs under maintainer CI (task helm-*)")
	}
	// chart lives at repo-root/chart; this test package is internal/controller.
	out, err := exec.Command("helm", "template", "../../chart",
		"--set", "observability.metrics.agentPodMonitor.enabled=true").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	for _, doc := range strings.Split(string(out), "\n---") {
		if !strings.Contains(doc, "kind: PodMonitor") {
			continue
		}
		var pm podMonitorSelector
		if err := yaml.Unmarshal([]byte(doc), &pm); err != nil {
			t.Fatalf("decode PodMonitor doc: %v\n%s", err, doc)
		}
		if pm.Kind == "PodMonitor" {
			return pm
		}
	}
	t.Fatalf("no PodMonitor rendered with agentPodMonitor.enabled=true; helm output:\n%s", out)
	return podMonitorSelector{}
}

// TestAgentPodMonitorSelectorMatchesGoConstants is the cross-boundary contract:
// the chart's rendered selector must equal the Go constants the operator stamps.
//
// This is the CORE guard the issue asks for. If AgentAppName or PartOfValue is
// renamed in Go without editing agent-podmonitor.yaml (or vice-versa), the
// rendered literal no longer equals the constant and this fails — the exact
// one-sided-change scenario neither prior test caught.
func TestAgentPodMonitorSelectorMatchesGoConstants(t *testing.T) {
	pm := renderAgentPodMonitor(t)

	if got := pm.Spec.Selector.MatchLabels[LabelAppName]; got != AgentAppName {
		t.Errorf("chart PodMonitor selector %s=%q does not match Go constant AgentAppName=%q; "+
			"a one-sided rename would silently break agent /metrics scraping",
			LabelAppName, got, AgentAppName)
	}
	if got := pm.Spec.Selector.MatchLabels[LabelPartOf]; got != PartOfValue {
		t.Errorf("chart PodMonitor selector %s=%q does not match Go constant PartOfValue=%q; "+
			"a one-sided rename would silently break agent /metrics scraping",
			LabelPartOf, got, PartOfValue)
	}
}

// TestAgentPodMonitorSelectorIsExactlyTheStampedLabels asserts the selector is a
// SUBSET of the labels buildDeployment stamps on the pod template — i.e. every
// selector key/value the PodMonitor requires is actually present on real agent
// pods. This catches a selector that adds an extra matchLabel the operator never
// stamps (which would select zero pods) as well as a value drift.
func TestAgentPodMonitorSelectorIsExactlyTheStampedLabels(t *testing.T) {
	pm := renderAgentPodMonitor(t)

	// The labels buildDeployment actually puts on the agent pod template.
	stamped := map[string]string{
		LabelAppName: AgentAppName,
		LabelPartOf:  PartOfValue,
	}
	if len(pm.Spec.Selector.MatchLabels) == 0 {
		t.Fatal("PodMonitor selector.matchLabels is empty; it would select every pod")
	}
	for k, v := range pm.Spec.Selector.MatchLabels {
		want, ok := stamped[k]
		if !ok {
			t.Errorf("PodMonitor selects on %q=%q, which buildDeployment never stamps on agent pods; "+
				"this selector would match zero agents", k, v)
			continue
		}
		if v != want {
			t.Errorf("PodMonitor selector %s=%q, but agent pods carry %s=%q", k, v, k, want)
		}
	}
}

// TestAgentPodMonitorNotRenderedWhenDisabled is the BOUNDARY case: with the
// opt-in flag off (the default) no PodMonitor is rendered, so the contract guard
// above is scoped strictly to the enabled path and never asserts against a
// phantom document.
func TestAgentPodMonitorNotRenderedWhenDisabled(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm binary not found on PATH; runs under maintainer CI")
	}
	out, err := exec.Command("helm", "template", "../../chart").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "kind: PodMonitor") {
		t.Error("PodMonitor must NOT render when agentPodMonitor.enabled is false (default)")
	}
}

// TestAgentPodMonitorRenderIsDeterministic is the IDEMPOTENCY / DETERMINISM
// category: the guard is only meaningful if the render is stable, so two renders
// must produce the same selector.
func TestAgentPodMonitorRenderIsDeterministic(t *testing.T) {
	a := renderAgentPodMonitor(t)
	b := renderAgentPodMonitor(t)
	if a.Spec.Selector.MatchLabels[LabelAppName] != b.Spec.Selector.MatchLabels[LabelAppName] ||
		a.Spec.Selector.MatchLabels[LabelPartOf] != b.Spec.Selector.MatchLabels[LabelPartOf] {
		t.Errorf("helm render is non-deterministic: %v vs %v",
			a.Spec.Selector.MatchLabels, b.Spec.Selector.MatchLabels)
	}
}

// CONCURRENCY / RACE — N/A. This suite shells out to `helm template` (an external
// process) and asserts against pure, immutable package constants; there is no
// shared mutable state to race. The Go-side render path (buildDeployment) is
// covered for concurrency by TestAgentProbesConcurrentRenderSafe in the deploy
// tests. No concurrency test is added here.
//
// INTEGRATION (envtest) — N/A. This is a chart-render contract, not a
// live-cluster behavior: `helm template` is a hermetic, offline render needing
// no apiserver. A running-cluster analogue (Prometheus actually scraping agent
// pods) would require a full monitoring stack and is out of scope for a DRY-pair
// guard. The maintainer's envtest suite already asserts the stamped pod labels
// (test/envtest towonelagent suite) when KUBEBUILDER_ASSETS is set.

// TestBuildDeploymentAgentLabelsSatisfyRenderedSelector ties the two sides
// together end-to-end in one assertion: a rendered agent Deployment's pod
// template labels must satisfy the rendered chart selector. This is the
// behavioral contract ("scraping works") expressed directly, independent of
// which constant names carry the values.
func TestBuildDeploymentAgentLabelsSatisfyRenderedSelector(t *testing.T) {
	pm := renderAgentPodMonitor(t)

	ta := renderAgent()
	cfg, _ := renderConfig(ta, allocsFor(), "inv-1")
	podLabels := buildDeployment(ta, cfg).Spec.Template.Labels

	for k, v := range pm.Spec.Selector.MatchLabels {
		if podLabels[k] != v {
			t.Errorf("rendered PodMonitor requires pod label %s=%q, but the agent pod template has %s=%q; "+
				"metrics scraping would select no pods", k, v, k, podLabels[k])
		}
	}
}
