package controller

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/towonel"
	"github.com/jacaudi/towonel-operator/internal/towonel/towoneltest"
)

func regionTunnel(inviteID string) *towonelv1alpha1.TowonelTunnel {
	tt := &towonelv1alpha1.TowonelTunnel{Name: "t1", Namespace: "ns"}
	tt.Status.InviteID = inviteID
	return tt
}

func TestPopulateRegionEndpointsSuccess(t *testing.T) {
	hub := towoneltest.NewHub()
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)
	hub.Seed(towonel.Invite{InviteID: "inv-1", TenantID: "ten-1", Name: "ns/t1"})
	hub.SeedInviteStatus(towonel.InviteStatus{InviteID: "inv-1", TenantID: "ten-1", Regions: []towonel.RegionStatus{
		{Region: "CA", Role: "primary", Edges: []towonel.EdgeStatus{
			{NodeID: "n1", PublicIPs: []string{"203.0.113.2", "203.0.113.1"}},
			{NodeID: "n2", PublicIPs: []string{"203.0.113.1"}},
		}},
		{Region: "EU", Role: "failover", Edges: []towonel.EdgeStatus{}},
	}})
	tt := regionTunnel("inv-1")
	(&TowonelTunnelReconciler{}).populateRegionEndpoints(t.Context(), tc, tt)
	want := []towonelv1alpha1.RegionEndpoint{
		{Region: "CA", Role: "primary", Addresses: []string{"203.0.113.1", "203.0.113.2"}},
		{Region: "EU", Role: "secondary"},
	}
	// Non-empty assertion guards the fake hub's /status route against shadowing (#38 §6).
	if !slices.EqualFunc(tt.Status.RegionEndpoints, want, func(a, b towonelv1alpha1.RegionEndpoint) bool {
		return a.Region == b.Region && a.Role == b.Role && slices.Equal(a.Addresses, b.Addresses)
	}) {
		t.Fatalf("regionEndpoints = %+v, want %+v", tt.Status.RegionEndpoints, want)
	}
	cond := meta.FindStatusCondition(tt.Status.Conditions, CondRegionEndpointsAvailable)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != ReasonSynced || cond.Message != "2 region(s), 1 with live edge(s)" {
		t.Fatalf("condition = %+v", cond)
	}
}

func TestPopulateRegionEndpoints404KeepsStatus(t *testing.T) {
	hub := towoneltest.NewHub()
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)
	hub.Seed(towonel.Invite{InviteID: "inv-1", TenantID: "ten-1", Name: "ns/t1"})
	hub.SetStatusUnavailable(true)
	tt := regionTunnel("inv-1")
	prior := []towonelv1alpha1.RegionEndpoint{{Region: "EU", Role: "primary", Addresses: []string{"198.51.100.9"}}}
	tt.Status.RegionEndpoints = prior
	(&TowonelTunnelReconciler{}).populateRegionEndpoints(t.Context(), tc, tt)
	if len(tt.Status.RegionEndpoints) != 1 || tt.Status.RegionEndpoints[0].Addresses[0] != "198.51.100.9" {
		t.Fatalf("regionEndpoints changed on 404: %+v", tt.Status.RegionEndpoints)
	}
	cond := meta.FindStatusCondition(tt.Status.Conditions, CondRegionEndpointsAvailable)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != ReasonUnavailable {
		t.Fatalf("condition = %+v, want False/Unavailable", cond)
	}
	// A 404 is also what an unknown invite returns, so the message must not blame the hub version alone.
	if want := "hub returned 404 for /v1/invites/{id}/status (hub too old, or invite unknown)"; cond.Message != want {
		t.Fatalf("condition message = %q, want %q", cond.Message, want)
	}
	if meta.FindStatusCondition(tt.Status.Conditions, CondReady) != nil {
		t.Fatal("populateRegionEndpoints must not touch Ready")
	}
}

func TestPopulateRegionEndpointsTransientErrorKeepsLastKnown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	t.Cleanup(srv.Close)
	tc := towonel.NewClient(srv.URL, "k", srv.Client())
	tt := regionTunnel("inv-1")
	tt.Status.RegionEndpoints = []towonelv1alpha1.RegionEndpoint{{Region: "EU", Role: "primary"}}
	(&TowonelTunnelReconciler{}).populateRegionEndpoints(t.Context(), tc, tt)
	if len(tt.Status.RegionEndpoints) != 1 {
		t.Fatalf("last-known regionEndpoints dropped: %+v", tt.Status.RegionEndpoints)
	}
	if cond := meta.FindStatusCondition(tt.Status.Conditions, CondRegionEndpointsAvailable); cond == nil || cond.Reason != ReasonAPIError {
		t.Fatalf("condition = %+v, want False/APIError", cond)
	}
}

func TestPopulateRegionEndpointsSkipsWithoutInvite(t *testing.T) {
	tt := regionTunnel("")
	(&TowonelTunnelReconciler{}).populateRegionEndpoints(t.Context(), nil, tt)
	if meta.FindStatusCondition(tt.Status.Conditions, CondRegionEndpointsAvailable) != nil {
		t.Fatal("no invite: must not set a condition")
	}
}

func TestMapRole(t *testing.T) {
	for in, want := range map[string]string{"primary": "primary", "failover": "secondary", "tertiary": "tertiary"} {
		if got := mapRole(in); got != want {
			t.Errorf("mapRole(%q) = %q, want %q", in, got, want)
		}
	}
}
