package towonel

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient spins up an httptest server whose handler is h, and returns a
// Client pointed at it plus the server (caller defers srv.Close via t.Cleanup).
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "test-key", srv.Client())
}
