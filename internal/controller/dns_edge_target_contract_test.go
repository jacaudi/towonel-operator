package controller

import (
	"maps"
	"os/exec"
	"slices"
	"testing"
)

// TestChartDNSEdgeTargetsRoundTripThroughResolver is the chart/Go contract for
// --dns-edge-target (design §6.1): default values render the public hub URL
// (which must equal DefaultTowonelAPIURL, or the public-hub defaults silently
// switch off) and no --dns-edge-target; a dns.edgeTargets map renders one arg
// per region that ResolveDNSEdgeTargets reads back.
func TestChartDNSEdgeTargetsRoundTripThroughResolver(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm binary not found on PATH; chart render guard runs under maintainer CI")
	}
	out, err := exec.Command("helm", "template", "../../chart").CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	if got := renderedManagerArgs(t, string(out), "--towonel-api-url="); !slices.Equal(got, []string{DefaultTowonelAPIURL}) {
		t.Fatalf("default --towonel-api-url = %v, want [%s]", got, DefaultTowonelAPIURL)
	}
	if got := renderedManagerArgs(t, string(out), "--dns-edge-target="); len(got) != 0 {
		t.Fatalf("default values must render no --dns-edge-target, got %v", got)
	}

	out, err = exec.Command("helm", "template", "../../chart", "--set-json",
		`dns.edgeTargets={"EU":"eu.example.com","CA":"edge.example.com."}`).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	args := renderedManagerArgs(t, string(out), "--dns-edge-target=")
	if want := []string{"CA=edge.example.com.", "EU=eu.example.com"}; !slices.Equal(args, want) {
		t.Fatalf("rendered --dns-edge-target args = %v, want %v", args, want)
	}
	got, err := ResolveDNSEdgeTargets(DefaultTowonelAPIURL, args)
	if err != nil {
		t.Fatalf("manager would reject the chart-rendered args: %v", err)
	}
	if want := map[string]string{"CA": "edge.example.com", "EU": "eu.example.com"}; !maps.Equal(got, want) {
		t.Fatalf("resolved = %v, want %v", got, want)
	}
}
