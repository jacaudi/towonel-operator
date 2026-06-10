package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/towonel"
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

// hubCall runs one hub call under its own deadline (design §3.1: per-call
// timeouts replace P3's single shared budget — convergePorts loops N calls).
func hubCall[T any](ctx context.Context, f func(context.Context) (T, error)) (T, error) {
	cctx, cancel := context.WithTimeout(ctx, hubCallTimeout)
	defer cancel()
	return f(cctx)
}

func allocKey(protocol, name string) string { return protocol + "/" + name }

func allocFromResponse(name string, resp *towonel.ReservePortResponse) towonelv1alpha1.PortAllocation {
	pa := towonelv1alpha1.PortAllocation{Name: name, Protocol: resp.Protocol, ListenPort: resp.Port}
	if resp.Edge != nil {
		pa.Edge = towonelv1alpha1.EdgeRef{NodeID: resp.Edge.NodeID, Addresses: slices.Clone(resp.Edge.Addresses)}
	}
	return pa
}

func edgesUnion(allocs []towonelv1alpha1.PortAllocation) []string {
	var out []string
	for _, a := range allocs {
		out = append(out, a.Edge.Addresses...)
	}
	return dedupe(out)
}

// convergePorts reconciles tenant port reservations to the agents' desired
// (protocol, name) union: adopt by label+protocol -> reserve -> prune strictly
// after -> publish from response bodies (design §4.B). Sets PortsReserved.
func (r *TowonelTunnelReconciler) convergePorts(ctx context.Context, tc *towonel.Client, tt *towonelv1alpha1.TowonelTunnel, agents []towonelv1alpha1.TowonelAgent) error {
	log := logf.FromContext(ctx)
	desired, conflicts := desiredPorts(agents)

	observed := map[string]towonelv1alpha1.PortAllocation{}
	for _, pa := range tt.Status.PortAllocations {
		observed[allocKey(pa.Protocol, pa.Name)] = pa
	}

	// Lazy, best-effort list for adoption (shape UNVERIFIED -> tolerate failure).
	var listed []towonel.ReservePortResponse
	listedOnce := false
	listReservations := func() []towonel.ReservePortResponse {
		if !listedOnce {
			listedOnce = true
			var err error
			listed, err = hubCall(ctx, func(c context.Context) ([]towonel.ReservePortResponse, error) {
				return tc.ListPorts(c, tt.Status.TenantID)
			})
			if err != nil {
				log.V(1).Info("list ports failed; skipping adoption", "err", err.Error())
				listed = nil
			}
		}
		return listed
	}

	var errs []error
	allocs := make([]towonelv1alpha1.PortAllocation, 0, len(desired))
	desiredKeys := map[string]bool{}
	for _, d := range desired {
		key := allocKey(d.protocol, d.name)
		desiredKeys[key] = true
		if pa, ok := observed[key]; ok {
			allocs = append(allocs, pa) // stability: never re-reserve on preferred change
			continue
		}
		// Adopt: label AND protocol must match (label alone is ambiguous for
		// a name reserved as both tcp and udp — design §4.B step 2).
		label := portLabel(tt.Namespace, tt.Name, d.name)
		adopted := false
		reservations := listReservations()
		for i := range reservations {
			res := &reservations[i]
			if res.Protocol == d.protocol && res.Label != nil && *res.Label == label {
				allocs = append(allocs, allocFromResponse(d.name, res))
				adopted = true
				break
			}
		}
		if adopted {
			continue
		}
		req := towonel.ReservePortRequest{Protocol: d.protocol, Label: new(label)}
		if d.preferred != 0 {
			req.Preferred = new(d.preferred)
		}
		resp, err := hubCall(ctx, func(c context.Context) (*towonel.ReservePortResponse, error) {
			return tc.ReservePort(c, tt.Status.TenantID, req)
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("reserve %s: %w", key, err))
			continue // non-blocking: siblings still reserve (design §3.3)
		}
		allocs = append(allocs, allocFromResponse(d.name, resp))
	}

	// Prune strictly AFTER the adopt/reserve loop (CF-op P3 invariant).
	for key, pa := range observed {
		if desiredKeys[key] {
			continue
		}
		_, err := hubCall(ctx, func(c context.Context) (struct{}, error) {
			return struct{}{}, tc.ReleasePort(c, tt.Status.TenantID, pa.Protocol, pa.ListenPort)
		})
		if err != nil {
			if apiErr, ok := errors.AsType[*towonel.APIError](err); !ok || apiErr.StatusCode != http.StatusNotFound {
				errs = append(errs, fmt.Errorf("release %s: %w", key, err))
				allocs = append(allocs, pa) // keep in status; retry next pass
			}
		}
	}

	slices.SortFunc(allocs, func(a, b towonelv1alpha1.PortAllocation) int {
		return cmp.Compare(allocKey(a.Protocol, a.Name), allocKey(b.Protocol, b.Name))
	})
	tt.Status.PortAllocations = allocs
	tt.Status.Edges = edgesUnion(allocs)

	if err := errors.Join(errs...); err != nil {
		setCond(tt, CondPortsReserved, metav1.ConditionFalse, ReasonAPIError, err.Error())
		return err
	}
	if len(conflicts) > 0 {
		msg := "preferredPort conflicts (first agent in ns/name order wins): " + strings.Join(conflicts, "; ")
		prev := meta.FindStatusCondition(tt.Status.Conditions, CondPortsReserved)
		if (prev == nil || prev.Message != msg) && r.Recorder != nil { // transition-gated Event (design §4.B step 1)
			r.Recorder.Event(tt, corev1.EventTypeWarning, ReasonPortConflict, msg)
		}
		setCond(tt, CondPortsReserved, metav1.ConditionTrue, ReasonPortConflict, msg)
		return nil
	}
	setCond(tt, CondPortsReserved, metav1.ConditionTrue, ReasonSynced, "port reservations converged")
	return nil
}
