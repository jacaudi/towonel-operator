package controller

import (
	"maps"
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestResolveDNSEdgeTargets(t *testing.T) {
	public := map[string]string{"CA": "edge-na.towonel.dev", "EU": "edge-eu.towonel.dev"}
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 63) // 255 chars
	ok := []struct {
		name    string
		url     string
		entries []string
		want    map[string]string
	}{
		{"public hub, no flags → defaults", DefaultTowonelAPIURL, nil, public},
		{"public hub with trailing slash → defaults", DefaultTowonelAPIURL + "/", nil, public},
		{"custom hub, no flags → none", "https://hub.example.com", nil, map[string]string{}},
		{"flags replace defaults", DefaultTowonelAPIURL, []string{"CA=x.example.com"}, map[string]string{"CA": "x.example.com"}},
		{"region upper-cased, trailing dot trimmed", "https://hub.example.com", []string{"ca=edge.example.com.", "EU=eu.example.com"},
			map[string]string{"CA": "edge.example.com", "EU": "eu.example.com"}},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveDNSEdgeTargets(tc.url, tc.entries)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got == nil || !maps.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	bad := map[string][]string{
		"missing =":            {"CA"},
		"empty region":         {"=x.example.com"},
		"uppercase fqdn":       {"CA=Bad.Example.com"},
		"single-label fqdn":    {"CA=localhost"},
		"over-long fqdn":       {"CA=" + long},
		"duplicate (any case)": {"CA=a.example.com", "ca=b.example.com"},
	}
	for name, entries := range bad {
		t.Run("rejects "+name, func(t *testing.T) {
			if got, err := ResolveDNSEdgeTargets("https://hub.example.com", entries); err == nil {
				t.Fatalf("want error, got %v", got)
			}
		})
	}
}

// The defaults are never a shared mutable global (Go standards): mutating one
// result must not leak into the next.
func TestResolveDNSEdgeTargetsReturnsFreshMaps(t *testing.T) {
	m1, _ := ResolveDNSEdgeTargets(DefaultTowonelAPIURL, nil)
	m1["CA"] = "mutated.example.com"
	m2, _ := ResolveDNSEdgeTargets(DefaultTowonelAPIURL, nil)
	if m2["CA"] != "edge-na.towonel.dev" {
		t.Fatalf("defaults leaked a mutation: %v", m2)
	}
}

// TestEdgeFQDNPatternMatchesCRD pins the flag validator to the generated CRD:
// the CRD's spec.dns.cnameTarget pattern is edgeFQDNPattern plus an optional
// trailing dot (the flag parser trims that dot before matching). If either
// side changes alone, this fails.
func TestEdgeFQDNPatternMatchesCRD(t *testing.T) {
	raw, err := os.ReadFile("../../config/crd/bases/towonel.io_towoneltunnels.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var crd struct {
		Spec struct {
			Versions []struct {
				Schema struct {
					OpenAPIV3Schema struct {
						Properties map[string]struct {
							Properties map[string]struct {
								Properties map[string]struct {
									Pattern string `json:"pattern"`
								} `json:"properties"`
							} `json:"properties"`
						} `json:"properties"`
					} `json:"openAPIV3Schema"`
				} `json:"schema"`
			} `json:"versions"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	if len(crd.Spec.Versions) == 0 {
		t.Fatal("no versions in the TowonelTunnel CRD")
	}
	got := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["dns"].Properties["cnameTarget"].Pattern
	want := strings.TrimSuffix(edgeFQDNPattern.String(), "$") + `\.?$`
	if got != want {
		t.Fatalf("CRD cnameTarget pattern = %q, want %q (edgeFQDNPattern + optional trailing dot)", got, want)
	}
}
