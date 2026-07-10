package controller

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jacaudi/towonel-operator/internal/towonel"
)

// rateLimitRequeue lets the reconciler honor a hub 429's Retry-After hint by
// requeueing after exactly that long, instead of returning the error and
// letting controller-runtime's generic exponential backoff hammer the shared
// per-tenant rate budget (issue #45). It only fires for a rate-limited
// APIError that carries a positive hint; every other error falls through to
// the normal error path.
func TestRateLimitRequeue(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantOK    bool
		wantAfter time.Duration
	}{
		{
			name:      "429 with hint requeues after the hint",
			err:       &towonel.APIError{StatusCode: http.StatusTooManyRequests, RetryAfter: 3 * time.Second},
			wantOK:    true,
			wantAfter: 3 * time.Second,
		},
		{
			name:      "wrapped 429 with hint is unwrapped and honored",
			err:       fmt.Errorf("converge hostnames: %w", &towonel.APIError{StatusCode: http.StatusTooManyRequests, RetryAfter: 7 * time.Second}),
			wantOK:    true,
			wantAfter: 7 * time.Second,
		},
		{
			name:   "429 without a hint falls through to the error path",
			err:    &towonel.APIError{StatusCode: http.StatusTooManyRequests},
			wantOK: false,
		},
		{
			name:   "non-429 API error falls through",
			err:    &towonel.APIError{StatusCode: http.StatusForbidden, RetryAfter: 5 * time.Second},
			wantOK: false,
		},
		{
			name:   "non-API error falls through",
			err:    errors.New("boom"),
			wantOK: false,
		},
		{
			name:   "nil error falls through",
			err:    nil,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, ok := rateLimitRequeue(tc.err)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if res.RequeueAfter != tc.wantAfter {
				t.Errorf("RequeueAfter = %v, want %v", res.RequeueAfter, tc.wantAfter)
			}
		})
	}
}
