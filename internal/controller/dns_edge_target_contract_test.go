package controller

import (
	"maps"
	"slices"
	"testing"
)

// TestChartDNSEdgeTargetsRoundTripThroughResolver is the chart/Go contract for
// --dns-edge-target (design §6.1): default values render the public hub URL
// (which must equal DefaultTowonelAPIURL, or the public-hub defaults silently
// switch off) and no --dns-edge-target; a dns.edgeTargets map renders one arg
// per region that ResolveDNSEdgeTargets reads back.
func TestChartDNSEdgeTargetsRoundTripThroughResolver(t *testing.T) {
	out := helmTemplate(t)
	if got := renderedManagerArgs(t, out, "--towonel-api-url="); !slices.Equal(got, []string{DefaultTowonelAPIURL}) {
		t.Fatalf("default --towonel-api-url = %v, want [%s]", got, DefaultTowonelAPIURL)
	}
	if got := renderedManagerArgs(t, out, "--dns-edge-target="); len(got) != 0 {
		t.Fatalf("default values must render no --dns-edge-target, got %v", got)
	}

	out = helmTemplate(t, "--set-json", `dns.edgeTargets={"EU":"eu.example.com","CA":"edge.example.com."}`)
	args := renderedManagerArgs(t, out, "--dns-edge-target=")
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
