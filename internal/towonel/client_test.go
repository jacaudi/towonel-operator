package towonel

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestClient_SendsAuthHeaderAndBaseURL(t *testing.T) {
	var gotAuth, gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	var out struct{}
	if err := c.do(context.Background(), http.MethodGet, "/v1/auth/me", nil, &out); err != nil {
		t.Fatalf("do() error = %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotPath != "/v1/auth/me" {
		t.Errorf("path = %q, want /v1/auth/me", gotPath)
	}
}

func TestClient_Non2xxReturnsAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	})

	err := c.do(context.Background(), http.MethodGet, "/v1/auth/me", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
	if apiErr.Body != `{"error":"nope"}` {
		t.Errorf("Body = %q", apiErr.Body)
	}
}
