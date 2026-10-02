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
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm binary not found on PATH; chart render guard runs under maintainer CI")
	}
	out, err := exec.Command("helm", "template", "../../chart", "--set-json",
		`agentEnv={"RUST_LOG":"info,iroh::socket::transports=trace","A_FIRST":"1"}`).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	got, err := ParseAgentEnv(renderedAgentEnvArgs(t, string(out)))
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
	out, err = exec.Command("helm", "template", "../../chart").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	if args := renderedAgentEnvArgs(t, string(out)); len(args) != 0 {
		t.Fatalf("no agentEnv must render no --agent-env args, got %v", args)
	}
}

// renderedAgentEnvArgs returns the NAME=VALUE payload of every --agent-env arg
// on the rendered manager Deployment.
func renderedAgentEnvArgs(t *testing.T, rendered string) []string {
	t.Helper()
	const flagPrefix = "--agent-env="
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
