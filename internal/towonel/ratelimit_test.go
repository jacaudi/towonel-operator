package towonel

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// parseRetryAfter is the pure signal parser behind the 429 handling. These
// cases lock down the paths the round-trip test can't easily reach: header
// whitespace, malformed/negative headers falling through to the body, the
// unsupported HTTP-date header form (safe degradation to the body/zero), and
// that the first "Wait for" match wins.
func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		header string
		body   string
		want   time.Duration
	}{
		{"header with surrounding whitespace", "  4  ", "", 4 * time.Second},
		{"malformed header falls back to body", "soon", "Wait for 6s", 6 * time.Second},
		{"negative header falls back to body", "-5", "Wait for 2s", 2 * time.Second},
		{"http-date header is unsupported, degrades to body", "Wed, 21 Oct 2026 07:28:00 GMT", "Wait for 8s", 8 * time.Second},
		{"http-date header with no body hint yields zero", "Wed, 21 Oct 2026 07:28:00 GMT", "", 0},
		{"first wait hint wins", "", "Wait for 3s then Wait for 30s", 3 * time.Second},
		{"no signal at all", "", "totally unrelated body", 0},
		{"empty inputs", "", "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfter(tc.header, tc.body); got != tc.want {
				t.Errorf("parseRetryAfter(%q, %q) = %v, want %v", tc.header, tc.body, got, tc.want)
			}
		})
	}
}

// On a 429 the hub signals how long to wait before retrying. The client must
// surface that hint on APIError.RetryAfter so the reconciler can back off for
// exactly that long instead of hammering the shared per-tenant rate budget
// (issue #45). Two signal shapes are honored: the standard Retry-After header
// (delta-seconds) and the hub's body text "... Wait for Ns".
func TestClient_429SurfacesRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header string // Retry-After header value ("" = unset)
		body   string
		want   time.Duration
	}{
		{
			name:   "retry-after header in delta-seconds",
			status: http.StatusTooManyRequests,
			header: "3",
			body:   `{"error":"rate_limited"}`,
			want:   3 * time.Second,
		},
		{
			name:   "body wait hint when header absent",
			status: http.StatusTooManyRequests,
			body:   "Too Many Requests! Wait for 5s",
			want:   5 * time.Second,
		},
		{
			name:   "header wins over body hint",
			status: http.StatusTooManyRequests,
			header: "2",
			body:   "Too Many Requests! Wait for 9s",
			want:   2 * time.Second,
		},
		{
			name:   "zero-second hint is treated as no hint",
			status: http.StatusTooManyRequests,
			body:   "Too Many Requests! Wait for 0s",
			want:   0,
		},
		{
			name:   "non-429 carries no retry-after",
			status: http.StatusForbidden,
			header: "3",
			body:   `{"error":"nope"}`,
			want:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set("Retry-After", tc.header)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})

			err := c.do(context.Background(), http.MethodGet, "/v1/auth/me", nil, nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error = %v, want *APIError", err)
			}
			if apiErr.RetryAfter != tc.want {
				t.Errorf("RetryAfter = %v, want %v", apiErr.RetryAfter, tc.want)
			}
		})
	}
}
