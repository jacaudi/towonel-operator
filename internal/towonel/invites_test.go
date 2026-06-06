package towonel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestCreateInvite(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody CreateInviteRequest
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"status":"ok","token":"tt_inv_2_abc","invite_id":"inv1","tenant_id":"ten1","name":"app"}`))
	})

	resp, err := c.CreateInvite(context.Background(), CreateInviteRequest{
		Hostnames: []string{"app.example.com"},
	})
	if err != nil {
		t.Fatalf("CreateInvite() error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/invites" {
		t.Errorf("request = %s %s, want POST /v1/invites", gotMethod, gotPath)
	}
	if len(gotBody.Hostnames) != 1 || gotBody.Hostnames[0] != "app.example.com" {
		t.Errorf("body hostnames = %v", gotBody.Hostnames)
	}
	if resp.Token != "tt_inv_2_abc" || resp.InviteID != "inv1" || resp.TenantID != "ten1" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestAddHostnames(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody AddHostnamesRequest
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"status":"ok","hostnames":["a.dev","b.dev"]}`))
	})

	resp, err := c.AddHostnames(context.Background(), "inv1", []string{"b.dev"})
	if err != nil {
		t.Fatalf("AddHostnames() error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/invites/inv1/hostnames" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if len(gotBody.Hostnames) != 1 || gotBody.Hostnames[0] != "b.dev" {
		t.Errorf("body = %v", gotBody.Hostnames)
	}
	if len(resp.Hostnames) != 2 {
		t.Errorf("resp hostnames = %v", resp.Hostnames)
	}
}

func TestRemoveHostname(t *testing.T) {
	var gotMethod, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	if err := c.RemoveHostname(context.Background(), "inv1", "a.dev"); err != nil {
		t.Fatalf("RemoveHostname() error = %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v1/invites/inv1/hostnames/a.dev" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
}

func TestDeleteInvite(t *testing.T) {
	var gotMethod, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	if err := c.DeleteInvite(context.Background(), "inv1"); err != nil {
		t.Fatalf("DeleteInvite() error = %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v1/invites/inv1" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
}

func TestListInvites(t *testing.T) {
	var gotMethod, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_, _ = w.Write([]byte(`[{"invite_id":"inv1","tenant_id":"ten1","name":"app","hostnames":["a.dev"],"region":"EU"}]`))
	})
	got, err := c.ListInvites(context.Background())
	if err != nil {
		t.Fatalf("ListInvites() error = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/invites" {
		t.Errorf("request = %s %s, want GET /v1/invites", gotMethod, gotPath)
	}
	if len(got) != 1 || got[0].InviteID != "inv1" || got[0].Region != "EU" {
		t.Errorf("got = %+v", got)
	}
}
