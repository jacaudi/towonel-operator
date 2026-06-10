package controller

import (
	"cmp"
	"fmt"
	"slices"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// desiredPort is one tunnel-global l4 service: same (protocol, name) across
// agents shares ONE public port (failover semantics, design §4.B).
type desiredPort struct {
	name      string
	protocol  string
	preferred int32
	// preferredBy is the ns/name of the agent whose non-zero preferredPort
	// won; empty while preferred is 0.
	preferredBy string
}

// desiredPorts unions tcp/udp entries across agents (sorted by ns/name for
// determinism). preferredPort: first non-zero wins; a differing non-zero
// loser is reported as a conflict string.
func desiredPorts(agents []towonelv1alpha1.TowonelAgent) ([]desiredPort, []string) {
	sorted := slices.Clone(agents)
	slices.SortFunc(sorted, func(a, b towonelv1alpha1.TowonelAgent) int {
		return cmp.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name)
	})
	byKey := map[string]*desiredPort{}
	var order []string
	var conflicts []string
	add := func(protocol string, e towonelv1alpha1.AgentL4Service, agentKey string) {
		key := protocol + "/" + e.Name
		cur, ok := byKey[key]
		if !ok {
			d := &desiredPort{name: e.Name, protocol: protocol, preferred: e.PreferredPort}
			if e.PreferredPort != 0 {
				d.preferredBy = agentKey
			}
			byKey[key] = d
			order = append(order, key)
			return
		}
		switch {
		case e.PreferredPort == 0 || e.PreferredPort == cur.preferred:
			// no-op: zero means no preference; equal means agreement
		case cur.preferred == 0:
			cur.preferred = e.PreferredPort
			cur.preferredBy = agentKey
		default:
			conflicts = append(conflicts, fmt.Sprintf("%s: %d (%s, kept) vs %d (%s)", key, cur.preferred, cur.preferredBy, e.PreferredPort, agentKey))
		}
	}
	for _, ag := range sorted {
		agentKey := ag.Namespace + "/" + ag.Name
		for _, e := range ag.Spec.TCP {
			add("tcp", e, agentKey)
		}
		for _, e := range ag.Spec.UDP {
			add("udp", e, agentKey)
		}
	}
	out := make([]desiredPort, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out, conflicts
}
