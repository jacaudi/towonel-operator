package towoneltest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jacaudi/towonel-operator/internal/towonel"
)

func TestHubAddHostnameConflict(t *testing.T) {
	hub := NewHub()
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)
	ctx := context.Background()

	created, err := tc.CreateInvite(ctx, towonel.CreateInviteRequest{Hostnames: []string{"a.example"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Re-adding a hostname already reserved on the invite must 409.
	_, err = tc.AddHostnames(ctx, created.InviteID, []string{"a.example"})
	apiErr, ok := errors.AsType[*towonel.APIError](err)
	if !ok || apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("re-add: want 409 APIError, got %v", err)
	}
	if !strings.Contains(apiErr.Body, "hostname_conflict") {
		t.Fatalf("re-add: body = %q, want hostname_conflict", apiErr.Body)
	}

	// A genuinely new hostname still succeeds.
	if _, err := tc.AddHostnames(ctx, created.InviteID, []string{"b.example"}); err != nil {
		t.Fatalf("add new: %v", err)
	}
}

func TestHubDeleteInviteFreesHostnames(t *testing.T) {
	hub := NewHub()
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)
	ctx := context.Background()

	first, err := tc.CreateInvite(ctx, towonel.CreateInviteRequest{Hostnames: []string{"a.example"}})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}

	// Deleting the invite must free its reserved hostnames.
	if err := tc.DeleteInvite(ctx, first.InviteID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// A new invite can now reclaim the freed hostname without a 409.
	second, err := tc.CreateInvite(ctx, towonel.CreateInviteRequest{})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if _, err := tc.AddHostnames(ctx, second.InviteID, []string{"a.example"}); err != nil {
		t.Fatalf("re-add after delete: want success, got %v", err)
	}
}

func TestHubPorts(t *testing.T) {
	h := NewHub()
	srv, c := h.Server()
	t.Cleanup(srv.Close)
	ctx := t.Context()

	// Reserve with preferred.
	resp, err := c.ReservePort(ctx, "ten-1", towonel.ReservePortRequest{
		Protocol: "tcp", Preferred: new(int32(4086)), Label: new("net/app/ssh"),
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if resp.Port != 4086 || resp.Protocol != "tcp" || resp.Edge == nil || len(resp.Edge.Addresses) == 0 {
		t.Fatalf("unexpected reservation: %+v", resp)
	}
	if resp.Label == nil || *resp.Label != "net/app/ssh" {
		t.Fatalf("label not round-tripped: %v", resp.Label)
	}

	// Same preferred again -> conflict error.
	if _, err := c.ReservePort(ctx, "ten-1", towonel.ReservePortRequest{
		Protocol: "tcp", Preferred: new(int32(4086)),
	}); err == nil {
		t.Fatal("expected conflict on taken preferred port")
	}

	// Globally seeded taken port -> conflict even for another tenant.
	h.SeedTakenPort("udp", 5000)
	if _, err := c.ReservePort(ctx, "ten-2", towonel.ReservePortRequest{
		Protocol: "udp", Preferred: new(int32(5000)),
	}); err == nil {
		t.Fatal("expected conflict on seeded taken port")
	}

	// No preferred -> allocated from range.
	resp2, err := c.ReservePort(ctx, "ten-1", towonel.ReservePortRequest{Protocol: "udp", Label: new("net/app/wg")})
	if err != nil {
		t.Fatalf("reserve auto: %v", err)
	}
	if resp2.Port < 30000 {
		t.Fatalf("auto port = %d, want >= 30000", resp2.Port)
	}

	// List is tenant-scoped.
	list, err := c.ListPorts(ctx, "ten-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListPorts len = %d, want 2", len(list))
	}

	// Release; second release is 404.
	if err := c.ReleasePort(ctx, "ten-1", "tcp", 4086); err != nil {
		t.Fatalf("release: %v", err)
	}
	err = c.ReleasePort(ctx, "ten-1", "tcp", 4086)
	var apiErr *towonel.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("second release: want 404 APIError, got %v", err)
	}
	if !h.HasReservation("ten-1", "udp", resp2.Port) {
		t.Fatal("udp reservation should remain")
	}
}
