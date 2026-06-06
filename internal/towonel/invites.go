package towonel

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// CreateInvite issues a new invite (agent enrollment token + authorized hostnames).
// POST /v1/invites
func (c *Client) CreateInvite(ctx context.Context, req CreateInviteRequest) (*CreateInviteResponse, error) {
	var out CreateInviteResponse
	if err := c.do(ctx, http.MethodPost, "/v1/invites", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListInvites returns invites visible to the caller. GET /v1/invites
func (c *Client) ListInvites(ctx context.Context) ([]Invite, error) {
	var out []Invite
	if err := c.do(ctx, http.MethodGet, "/v1/invites", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AddHostnames authorizes additional hostnames on an existing invite, without
// re-issuing the token. POST /v1/invites/{id}/hostnames
func (c *Client) AddHostnames(ctx context.Context, inviteID string, hostnames []string) (*AddHostnamesResponse, error) {
	var out AddHostnamesResponse
	path := fmt.Sprintf("/v1/invites/%s/hostnames", url.PathEscape(inviteID))
	if err := c.do(ctx, http.MethodPost, path, AddHostnamesRequest{Hostnames: hostnames}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveHostname de-authorizes a hostname from an invite.
// DELETE /v1/invites/{id}/hostnames/{hostname}
func (c *Client) RemoveHostname(ctx context.Context, inviteID, hostname string) error {
	path := fmt.Sprintf("/v1/invites/%s/hostnames/%s", url.PathEscape(inviteID), url.PathEscape(hostname))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// DeleteInvite revokes an invite and removes its tenant.
// DELETE /v1/invites/{id}
func (c *Client) DeleteInvite(ctx context.Context, inviteID string) error {
	path := fmt.Sprintf("/v1/invites/%s", url.PathEscape(inviteID))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
