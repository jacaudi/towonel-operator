package towonel

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestAPIError_Error(t *testing.T) {
	e := &APIError{StatusCode: http.StatusNotFound, Body: "missing"}
	want := "towonel api: status 404: missing"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestNewClient_NilHTTPClientDefaultsAndTrimsSlash(t *testing.T) {
	c := NewClient("https://hub.example/", "k", nil)
	if c.httpClient != http.DefaultClient {
		t.Errorf("httpClient = %v, want http.DefaultClient", c.httpClient)
	}
	if c.baseURL != "https://hub.example" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", c.baseURL)
	}
}

func TestDo_MarshalRequestError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be reached when request marshaling fails")
	})
	// A channel is not JSON-encodable, so json.Marshal fails before any I/O.
	err := c.do(context.Background(), http.MethodPost, "/x", make(chan int), nil)
	if err == nil {
		t.Fatal("do() error = nil, want marshal error")
	}
}

func TestDo_BuildRequestError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be reached when request building fails")
	})
	// A method containing a space is rejected by http.NewRequestWithContext.
	err := c.do(context.Background(), "BAD METHOD", "/x", nil, nil)
	if err == nil {
		t.Fatal("do() error = nil, want build-request error")
	}
}

func TestDo_RequestSendError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	// A canceled context makes httpClient.Do fail before a response is received.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.do(ctx, http.MethodGet, "/x", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("do() error = %v, want context.Canceled", err)
	}
}

func TestDo_DecodeResponseError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{not valid json"))
	})
	var out struct {
		A string `json:"a"`
	}
	err := c.do(context.Background(), http.MethodGet, "/x", nil, &out)
	if err == nil {
		t.Fatal("do() error = nil, want decode error")
	}
}

// TestOperations_PropagateAPIError exercises the error-return branch of every
// public operation: when the hub answers non-2xx, each must surface an *APIError
// (and a nil result for the value-returning ones).
func TestOperations_PropagateAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	})
	ctx := context.Background()

	ops := []struct {
		name string
		call func() error
	}{
		{"CreateInvite", func() error { _, e := c.CreateInvite(ctx, CreateInviteRequest{}); return e }},
		{"ListInvites", func() error { _, e := c.ListInvites(ctx); return e }},
		{"AddHostnames", func() error { _, e := c.AddHostnames(ctx, "inv1", []string{"a.example"}); return e }},
		{"RemoveHostname", func() error { _, e := c.RemoveHostname(ctx, "inv1", "a.example"); return e }},
		{"DeleteInvite", func() error { return c.DeleteInvite(ctx, "inv1") }},
		{"AvailablePorts", func() error { _, e := c.AvailablePorts(ctx, "tcp", 1); return e }},
		{"ReservePort", func() error { _, e := c.ReservePort(ctx, "ten1", ReservePortRequest{}); return e }},
		{"ListPorts", func() error { _, e := c.ListPorts(ctx, "ten1"); return e }},
		{"ReleasePort", func() error { return c.ReleasePort(ctx, "ten1", "tcp", 4086) }},
	}

	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			var apiErr *APIError
			if err := op.call(); !errors.As(err, &apiErr) {
				t.Fatalf("%s error = %v, want *APIError", op.name, err)
			} else if apiErr.StatusCode != http.StatusInternalServerError {
				t.Errorf("%s StatusCode = %d, want 500", op.name, apiErr.StatusCode)
			}
		})
	}
}
