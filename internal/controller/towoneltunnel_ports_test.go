package controller

import (
	"net/http/httptest"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	record "k8s.io/client-go/tools/record"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/towonel"
	"github.com/jacaudi/towonel-operator/internal/towonel/towoneltest"
)

func l4Agent(ns, name string, tcp, udp []towonelv1alpha1.AgentL4Service) towonelv1alpha1.TowonelAgent {
	return towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       towonelv1alpha1.TowonelAgentSpec{TCP: tcp, UDP: udp},
	}
}

func TestDesiredPorts(t *testing.T) {
	agents := []towonelv1alpha1.TowonelAgent{
		l4Agent("a", "one",
			[]towonelv1alpha1.AgentL4Service{
				{Name: "ssh", Origin: "a:22", PreferredPort: 2222},
				{Name: "game", Origin: "a:4086"},
			},
			[]towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "a:51820"}}, // same name, udp = distinct service
		),
		l4Agent("b", "two",
			[]towonelv1alpha1.AgentL4Service{
				{Name: "ssh", Origin: "b:22", PreferredPort: 2223}, // conflicts with a/one's 2222
				{Name: "game", Origin: "b:4086", PreferredPort: 4086},
			},
			nil,
		),
		// agrees with a/one's winning 2222 — agreement is not a conflict
		l4Agent("c", "three",
			[]towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "c:22", PreferredPort: 2222}},
			nil,
		),
	}
	desired, conflicts := desiredPorts(agents)

	keys := make([]string, 0, len(desired))
	for _, d := range desired {
		keys = append(keys, d.protocol+"/"+d.name)
	}
	slices.Sort(keys)
	if want := []string{"tcp/game", "tcp/ssh", "udp/ssh"}; !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for _, d := range desired {
		switch d.protocol + "/" + d.name {
		case "tcp/ssh":
			if d.preferred != 2222 { // first in ns/name order wins
				t.Errorf("tcp/ssh preferred = %d, want 2222", d.preferred)
			}
		case "tcp/game":
			if d.preferred != 4086 { // zero then non-zero -> non-zero adopted, no conflict
				t.Errorf("tcp/game preferred = %d, want 4086", d.preferred)
			}
		}
	}
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %v, want exactly one (tcp/ssh)", conflicts)
	}
}

func portsFixture(t *testing.T) (*TowonelTunnelReconciler, *towoneltest.Hub, *httptest.Server, *towonel.Client, *towonelv1alpha1.TowonelTunnel) {
	t.Helper()
	hub := towoneltest.NewHub()
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)
	tt := &towonelv1alpha1.TowonelTunnel{
		ObjectMeta: metav1.ObjectMeta{Namespace: "net", Name: "app"},
		Status:     towonelv1alpha1.TowonelTunnelStatus{InviteID: "inv-1", TenantID: "ten-1"},
	}
	return &TowonelTunnelReconciler{}, hub, srv, tc, tt
}

func TestConvergePortsReserveAndPublish(t *testing.T) {
	r, hub, _, tc, tt := portsFixture(t)
	agents := []towonelv1alpha1.TowonelAgent{
		l4Agent("a", "one",
			[]towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "a:22", PreferredPort: 2222}},
			[]towonelv1alpha1.AgentL4Service{{Name: "wg", Origin: "a:51820"}}),
	}
	if err := r.convergePorts(t.Context(), tc, tt, agents); err != nil {
		t.Fatalf("convergePorts: %v", err)
	}
	if n := len(tt.Status.PortAllocations); n != 2 {
		t.Fatalf("allocations = %d, want 2", n)
	}
	var ssh towonelv1alpha1.PortAllocation
	for _, pa := range tt.Status.PortAllocations {
		if pa.Name == "ssh" {
			ssh = pa
		}
	}
	if ssh.Protocol != "tcp" || ssh.ListenPort != 2222 || len(ssh.Edge.Addresses) == 0 {
		t.Fatalf("ssh allocation = %+v", ssh)
	}
	if !slices.Equal(tt.Status.Edges, []string{"203.0.113.10"}) {
		t.Fatalf("edges = %v", tt.Status.Edges)
	}
	if hub.ReservationCount("ten-1") != 2 {
		t.Fatalf("hub reservations = %d, want 2", hub.ReservationCount("ten-1"))
	}
	if !meta.IsStatusConditionTrue(tt.Status.Conditions, CondPortsReserved) {
		t.Fatal("PortsReserved should be True")
	}
}

func TestConvergePortsIdempotentAndPrune(t *testing.T) {
	r, hub, _, tc, tt := portsFixture(t)
	agents := []towonelv1alpha1.TowonelAgent{
		l4Agent("a", "one", []towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "a:22"}}, nil),
	}
	if err := r.convergePorts(t.Context(), tc, tt, agents); err != nil {
		t.Fatal(err)
	}
	if err := r.convergePorts(t.Context(), tc, tt, agents); err != nil { // no duplicate
		t.Fatal(err)
	}
	if hub.ReservationCount("ten-1") != 1 {
		t.Fatalf("idempotency: hub reservations = %d, want 1", hub.ReservationCount("ten-1"))
	}
	// Entry leaves the desired set -> pruned.
	if err := r.convergePorts(t.Context(), tc, tt, nil); err != nil {
		t.Fatal(err)
	}
	if hub.ReservationCount("ten-1") != 0 || len(tt.Status.PortAllocations) != 0 {
		t.Fatalf("prune failed: hub=%d status=%d", hub.ReservationCount("ten-1"), len(tt.Status.PortAllocations))
	}
}

func TestConvergePortsAdoptionByLabelAndProtocol(t *testing.T) {
	r, hub, _, tc, tt := portsFixture(t)
	// Same name reserved as BOTH protocols hub-side; adoption must match protocol.
	hub.SeedReservation("ten-1", towonel.ReservePortResponse{
		Port: 7100, Protocol: "tcp", Label: new(portLabel("net", "app", "dual")),
		Edge: &towonel.EdgeInfo{NodeID: "edge-1", Addresses: []string{"203.0.113.10"}},
	})
	hub.SeedReservation("ten-1", towonel.ReservePortResponse{
		Port: 7200, Protocol: "udp", Label: new(portLabel("net", "app", "dual")),
		Edge: &towonel.EdgeInfo{NodeID: "edge-1", Addresses: []string{"203.0.113.10"}},
	})
	agents := []towonelv1alpha1.TowonelAgent{
		l4Agent("a", "one",
			[]towonelv1alpha1.AgentL4Service{{Name: "dual", Origin: "a:1"}},
			[]towonelv1alpha1.AgentL4Service{{Name: "dual", Origin: "a:2"}}),
	}
	if err := r.convergePorts(t.Context(), tc, tt, agents); err != nil {
		t.Fatal(err)
	}
	if hub.ReservationCount("ten-1") != 2 { // adopted, not re-reserved
		t.Fatalf("adoption: hub reservations = %d, want 2", hub.ReservationCount("ten-1"))
	}
	for _, pa := range tt.Status.PortAllocations {
		if pa.Protocol == "tcp" && pa.ListenPort != 7100 {
			t.Errorf("tcp adopted port = %d, want 7100", pa.ListenPort)
		}
		if pa.Protocol == "udp" && pa.ListenPort != 7200 {
			t.Errorf("udp adopted port = %d, want 7200", pa.ListenPort)
		}
	}
}

func TestConvergePortsConflictEventOnce(t *testing.T) {
	r, _, _, tc, tt := portsFixture(t)
	rec := record.NewFakeRecorder(8)
	r.Recorder = rec
	agents := []towonelv1alpha1.TowonelAgent{
		l4Agent("a", "one", []towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "a:22", PreferredPort: 2222}}, nil),
		l4Agent("b", "two", []towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "b:22", PreferredPort: 2223}}, nil),
	}
	for range 2 { // two passes: the Warning Event must fire on transition only
		if err := r.convergePorts(t.Context(), tc, tt, agents); err != nil {
			t.Fatal(err)
		}
	}
	cond := meta.FindStatusCondition(tt.Status.Conditions, CondPortsReserved)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != ReasonPortConflict {
		t.Fatalf("PortsReserved = %+v, want True/%s", cond, ReasonPortConflict)
	}
	if got := len(rec.Events); got != 1 {
		t.Fatalf("events = %d, want exactly 1 (transition-gated)", got)
	}
}

func TestConvergePortsFailureIsolation(t *testing.T) {
	r, hub, _, tc, tt := portsFixture(t)
	hub.SeedTakenPort("tcp", 9999) // someone else holds the preferred port
	agents := []towonelv1alpha1.TowonelAgent{
		l4Agent("a", "one",
			[]towonelv1alpha1.AgentL4Service{
				{Name: "stuck", Origin: "a:1", PreferredPort: 9999},
				{Name: "fine", Origin: "a:2"},
			}, nil),
	}
	err := r.convergePorts(t.Context(), tc, tt, agents)
	if err == nil {
		t.Fatal("want error for the stuck reservation")
	}
	if len(tt.Status.PortAllocations) != 1 || tt.Status.PortAllocations[0].Name != "fine" {
		t.Fatalf("sibling should still reserve: %+v", tt.Status.PortAllocations)
	}
	if meta.IsStatusConditionTrue(tt.Status.Conditions, CondPortsReserved) {
		t.Fatal("PortsReserved should be False on partial failure")
	}
}
