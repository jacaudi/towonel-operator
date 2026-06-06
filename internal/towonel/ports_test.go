package towonel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestAvailablePorts(t *testing.T) {
	var gotPath, gotQuery string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_, _ = w.Write([]byte(`{"protocol":"tcp","range_start":20000,"range_end":21000,"ports":[20001,20002]}`))
	})
	resp, err := c.AvailablePorts(context.Background(), "tcp", 2)
	if err != nil {
		t.Fatalf("AvailablePorts() error = %v", err)
	}
	if gotPath != "/v1/ports/available" {
		t.Errorf("path = %s", gotPath)
	}
	if gotQuery != "count=2&protocol=tcp" {
		t.Errorf("query = %q, want count=2&protocol=tcp", gotQuery)
	}
	if len(resp.Ports) != 2 || resp.Ports[0] != 20001 {
		t.Errorf("resp = %+v", resp)
	}
}

func TestReservePort(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody ReservePortRequest
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"status":"ok","port":4086,"protocol":"tcp","claimed_at_ms":1700000000000,"edge":{"node_id":"e1","addresses":["1.2.3.4"]}}`))
	})
	pref := int32(4086)
	resp, err := c.ReservePort(context.Background(), "ten1", ReservePortRequest{Protocol: "tcp", Preferred: &pref})
	if err != nil {
		t.Fatalf("ReservePort() error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/tenants/ten1/ports" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody.Preferred == nil || *gotBody.Preferred != 4086 {
		t.Errorf("body preferred = %v", gotBody.Preferred)
	}
	if resp.Port != 4086 || resp.Edge == nil || resp.Edge.Addresses[0] != "1.2.3.4" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestListPorts(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`[{"status":"ok","port":4086,"protocol":"tcp","claimed_at_ms":1}]`))
	})
	got, err := c.ListPorts(context.Background(), "ten1")
	if err != nil {
		t.Fatalf("ListPorts() error = %v", err)
	}
	if gotPath != "/v1/tenants/ten1/ports" {
		t.Errorf("path = %s", gotPath)
	}
	if len(got) != 1 || got[0].Port != 4086 {
		t.Errorf("got = %+v", got)
	}
}

func TestReleasePort(t *testing.T) {
	var gotMethod, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	if err := c.ReleasePort(context.Background(), "ten1", "tcp", 4086); err != nil {
		t.Fatalf("ReleasePort() error = %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/v1/tenants/ten1/ports/tcp/4086" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
}
