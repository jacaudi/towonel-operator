package controller

import (
	"fmt"
	"regexp"
	"strings"
)

// edgeFQDNPattern mirrors the TunnelDNSSpec.CNAMETarget CRD pattern with the
// trailing dot already trimmed: lowercase DNS labels of 1–63 chars, two or more.
var edgeFQDNPattern = regexp.MustCompile(`^([a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?\.)+[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// publicHubEdgeTargets returns Towonel's public-hub regional edge names as a
// fresh map per call (never a shared mutable global). Sources (design §10.1):
// towonel.dev/docs/managed names edge-eu.towonel.dev; CA→edge-na.towonel.dev
// rests on working production records and DNS. If either is ever
// contradicted, change only this function.
func publicHubEdgeTargets() map[string]string {
	return map[string]string{"CA": "edge-na.towonel.dev", "EU": "edge-eu.towonel.dev"}
}

// ResolveDNSEdgeTargets turns the manager's repeated --dns-edge-target=REGION=FQDN
// flags into the region → CNAME-target map for spec.dns.mode DNSEndpoint
// tunnels (design §6.1). With no flags it returns publicHubEdgeTargets only
// when apiURL is the public hub, otherwise an empty map: a self-hosted hub must
// never silently CNAME to Towonel's public edges. Any flag replaces the
// defaults entirely. REGION is upper-cased (the hub upper-cases regions); one
// trailing dot is trimmed from FQDN. Malformed, invalid and duplicate entries
// are errors, so a misconfigured manager fails at startup (12-Factor III).
func ResolveDNSEdgeTargets(apiURL string, entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		if strings.TrimSuffix(apiURL, "/") == DefaultTowonelAPIURL {
			return publicHubEdgeTargets(), nil
		}
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		region, fqdn, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("dns edge target %q: want REGION=FQDN", entry)
		}
		region = strings.ToUpper(strings.TrimSpace(region))
		if region == "" {
			return nil, fmt.Errorf("dns edge target %q: empty REGION", entry)
		}
		fqdn = strings.TrimSuffix(fqdn, ".")
		if len(fqdn) > 253 || !edgeFQDNPattern.MatchString(fqdn) {
			return nil, fmt.Errorf("dns edge target %q: invalid FQDN %q (lowercase DNS name with two or more labels, at most 253 characters)", entry, fqdn)
		}
		if _, dup := out[region]; dup {
			return nil, fmt.Errorf("dns edge target %q: region %s set more than once", entry, region)
		}
		out[region] = fqdn
	}
	return out, nil
}
