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
		Hostnames: []string{"app.towonel.dev"},
	})
	if err != nil {
		t.Fatalf("CreateInvite() error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/invites" {
		t.Errorf("request = %s %s, want POST /v1/invites", gotMethod, gotPath)
	}
	if len(gotBody.Hostnames) != 1 || gotBody.Hostnames[0] != "app.towonel.dev" {
		t.Errorf("body hostnames = %v", gotBody.Hostnames)
	}
	if resp.Token != "tt_inv_2_abc" || resp.InviteID != "inv1" || resp.TenantID != "ten1" || resp.Name != "app" {
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
	if len(resp.Hostnames) != 2 || resp.Hostnames[0] != "a.dev" || resp.Hostnames[1] != "b.dev" {
		t.Errorf("resp hostnames = %v", resp.Hostnames)
	}
}

func TestRemoveHostname(t *testing.T) {
	var gotMethod, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_, _ = w.Write([]byte(`{"status":"ok","hostname":"a.dev","remaining_hostnames":["b.dev","c.dev"]}`))
	})
	resp, err := c.RemoveHostname(context.Background(), "inv1", "a.dev")
	if err != nil {
		t.Fatalf("RemoveHostname() error = %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v1/invites/inv1/hostnames/a.dev" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if resp.Status != "ok" {
		t.Errorf("resp.Status = %q, want %q", resp.Status, "ok")
	}
	if resp.Hostname != "a.dev" {
		t.Errorf("resp.Hostname = %q, want %q", resp.Hostname, "a.dev")
	}
	if len(resp.RemainingHostnames) != 2 || resp.RemainingHostnames[0] != "b.dev" || resp.RemainingHostnames[1] != "c.dev" {
		t.Errorf("resp.RemainingHostnames = %v, want [b.dev c.dev]", resp.RemainingHostnames)
	}
}

func TestRemoveHostname_EscapedPathParams(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"status":"ok","hostname":"host/name","remaining_hostnames":[]}`))
	})
	_, err := c.RemoveHostname(context.Background(), "inv/1", "host/name")
	if err != nil {
		t.Fatalf("RemoveHostname() error = %v", err)
	}
	if gotPath != "/v1/invites/inv%2F1/hostnames/host%2Fname" {
		t.Errorf("path = %s, want escaped segments", gotPath)
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

func TestGetInvite(t *testing.T) {
	var gotMethod, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_, _ = w.Write([]byte(`{"invite_id":"inv1","tenant_id":"ten1","name":"app","hostnames":["a.dev","b.dev"],"region":"EU"}`))
	})
	got, err := c.GetInvite(context.Background(), "inv1")
	if err != nil {
		t.Fatalf("GetInvite() error = %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/invites/inv1" {
		t.Errorf("request = %s %s, want GET /v1/invites/inv1", gotMethod, gotPath)
	}
	if got.InviteID != "inv1" || got.TenantID != "ten1" {
		t.Errorf("got = %+v", got)
	}
	if len(got.Hostnames) != 2 || got.Hostnames[0] != "a.dev" || got.Hostnames[1] != "b.dev" {
		t.Errorf("got.Hostnames = %v, want [a.dev b.dev]", got.Hostnames)
	}
}

func TestGetInvite_EscapedPathParam(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"invite_id":"inv/1","hostnames":[]}`))
	})
	if _, err := c.GetInvite(context.Background(), "inv/1"); err != nil {
		t.Fatalf("GetInvite() error = %v", err)
	}
	if gotPath != "/v1/invites/inv%2F1" {
		t.Errorf("path = %s, want escaped segment", gotPath)
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
