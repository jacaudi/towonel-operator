package controller

import (
	"os/exec"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/yaml"
)

// TestChartAgentEnvRoundTripsThroughFlagParser is the chart/Go contract for the
// operator-wide agent env: the chart renders .Values.agentEnv as repeated
// --agent-env=NAME=VALUE manager args, and ParseAgentEnv must read exactly those
// args back into the same variables — including a value carrying its own '='
// and ',' (RUST_LOG directives), the case a naive split or unquoted YAML breaks.
func TestChartAgentEnvRoundTripsThroughFlagParser(t *testing.T) {
	out := helmTemplate(t, "--set-json", `agentEnv={"RUST_LOG":"info,iroh::socket::transports=trace","A_FIRST":"1"}`)
	got, err := ParseAgentEnv(renderedManagerArgs(t, out, "--agent-env="))
	if err != nil {
		t.Fatalf("manager would reject the chart-rendered --agent-env args: %v", err)
	}
	want := []corev1.EnvVar{
		{Name: "A_FIRST", Value: "1"},
		{Name: "RUST_LOG", Value: "info,iroh::socket::transports=trace"},
	}
	if !equality.Semantic.DeepEqual(got, want) {
		t.Fatalf("chart-rendered agent env = %+v, want %+v", got, want)
	}

	// Default values: no --agent-env arg at all.
	if args := renderedManagerArgs(t, helmTemplate(t), "--agent-env="); len(args) != 0 {
		t.Fatalf("no agentEnv must render no --agent-env args, got %v", args)
	}
}

// helmTemplate renders the chart (repo-root/chart; this package is
// internal/controller) with the extra args and returns the output. It skips
// (not fails) when the helm binary is unavailable so `go test ./...` stays
// green in sandboxes without helm; the maintainer's CI has helm (it runs
// `task ci`), so the chart/Go contract guards execute there.
func helmTemplate(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm binary not found on PATH; chart render guard runs under maintainer CI")
	}
	out, err := exec.CommandContext(t.Context(), "helm", append([]string{"template", "../../chart"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	return string(out)
}

// renderedManagerArgs returns the payload after flagPrefix of every matching
// arg on the rendered manager Deployment's containers, in render order.
func renderedManagerArgs(t *testing.T, rendered, flagPrefix string) []string {
	t.Helper()
	var entries []string
	for doc := range strings.SplitSeq(rendered, "\n---") {
		if !strings.Contains(doc, "kind: Deployment") {
			continue
		}
		var dep struct {
			Spec struct {
				Template struct {
					Spec corev1.PodSpec `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal([]byte(doc), &dep); err != nil {
			t.Fatalf("decode Deployment doc: %v\n%s", err, doc)
		}
		for _, c := range dep.Spec.Template.Spec.Containers {
			for _, arg := range c.Args {
				if entry, ok := strings.CutPrefix(arg, flagPrefix); ok {
					entries = append(entries, entry)
				}
			}
		}
	}
	return entries
}
