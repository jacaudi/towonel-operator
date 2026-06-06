package towonel

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// AvailablePorts queries free ports for a protocol ("tcp"/"udp"). count is
// clamped by the server (1-200). GET /v1/ports/available
func (c *Client) AvailablePorts(ctx context.Context, protocol string, count int) (*AvailablePortsResponse, error) {
	q := url.Values{}
	q.Set("protocol", protocol)
	q.Set("count", strconv.Itoa(count))
	var out AvailablePortsResponse
	if err := c.do(ctx, http.MethodGet, "/v1/ports/available?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReservePort reserves a TCP/UDP port for a tenant.
// POST /v1/tenants/{id}/ports
func (c *Client) ReservePort(ctx context.Context, tenantID string, req ReservePortRequest) (*ReservePortResponse, error) {
	var out ReservePortResponse
	path := fmt.Sprintf("/v1/tenants/%s/ports", url.PathEscape(tenantID))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListPorts lists a tenant's port reservations. GET /v1/tenants/{id}/ports
func (c *Client) ListPorts(ctx context.Context, tenantID string) ([]ReservePortResponse, error) {
	var out []ReservePortResponse
	path := fmt.Sprintf("/v1/tenants/%s/ports", url.PathEscape(tenantID))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ReleasePort releases a reservation.
// DELETE /v1/tenants/{id}/ports/{proto}/{port}
func (c *Client) ReleasePort(ctx context.Context, tenantID, protocol string, port int32) error {
	path := fmt.Sprintf("/v1/tenants/%s/ports/%s/%d", url.PathEscape(tenantID), url.PathEscape(protocol), port)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
