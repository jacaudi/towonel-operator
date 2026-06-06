// Package towonel is a Go client for the Towonel user API
// (auth API-keys, invites, ports). See console.towonel.dev/api-docs/user.json.
package towonel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
}

func (e *APIError) Error() string {
	return fmt.Sprintf("towonel api: status %d: %s", e.StatusCode, e.Body)
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
		return &APIError{StatusCode: resp.StatusCode, Body: string(respBytes)}
	}

	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
