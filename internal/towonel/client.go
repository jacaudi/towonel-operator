// Package towonel is a Go client for the Towonel user API
// (auth API-keys, invites, ports). See hub.towonel.dev/api-docs/user.json.
package towonel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Client talks to a Towonel hub's user API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient returns a Client. baseURL is the hub root (no trailing slash
// required); apiKey is a Towonel API-key token; hc may be nil (defaults to
// http.DefaultClient). Note that http.DefaultClient has no timeout; callers
// should pass their own *http.Client with a timeout set, or always use a
// context with a deadline.
func NewClient(baseURL, apiKey string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: hc,
	}
}

// APIError is returned for non-2xx responses.
type APIError struct {
	StatusCode int
	Body       string
	// RetryAfter is how long the hub asked the client to wait before retrying.
	// It is populated only for 429 responses, from the Retry-After header
	// (delta-seconds) or, failing that, the hub's body "Wait for Ns" hint; it is
	// zero when no positive hint was given. Callers use it to back off for
	// exactly as long as the hub requested (issue #45).
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("towonel api: status %d: %s", e.StatusCode, e.Body)
}

// waitHintRe matches the hub's 429 body hint, e.g. "... Wait for 3s".
var waitHintRe = regexp.MustCompile(`Wait for (\d+)s`)

// parseRetryAfter extracts a retry delay from a 429 response, preferring the
// standard Retry-After header (delta-seconds) and falling back to the hub's
// body "Wait for Ns" text. It returns 0 when neither yields a positive delay.
func parseRetryAfter(header, body string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if m := waitHintRe.FindStringSubmatch(body); m != nil {
		if secs, err := strconv.Atoi(m[1]); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}

// do executes an API request. reqBody (if non-nil) is JSON-encoded; out (if
// non-nil) is JSON-decoded from a 2xx response body.
func (c *Client) do(ctx context.Context, method, path string, reqBody, out any) error {
	var body io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{StatusCode: resp.StatusCode, Body: string(respBytes)}
		if resp.StatusCode == http.StatusTooManyRequests {
			apiErr.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), apiErr.Body)
		}
		return apiErr
	}

	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
