package towonel

// CreateInviteRequest is the body of POST /v1/invites.
type CreateInviteRequest struct {
	Hostnames       []string `json:"hostnames"`                  // required
	Name            *string  `json:"name,omitempty"`             // optional label
	Region          *string  `json:"region,omitempty"`           // absent → EU
	FailoverRegions []string `json:"failover_regions,omitempty"` // extra dial regions
	ExpiresInSecs   *int64   `json:"expires_in_secs,omitempty"`  // nil/0 → never
	OwnerEmail      *string  `json:"owner_email,omitempty"`      // operator-key only
}

// CreateInviteResponse is returned by POST /v1/invites.
type CreateInviteResponse struct {
	Status      string `json:"status"`
	Token       string `json:"token"` // = TOWONEL_INVITE_TOKEN (shown once)
	InviteID    string `json:"invite_id"`
	TenantID    string `json:"tenant_id"`
	Name        string `json:"name"`
	ExpiresAtMs *int64 `json:"expires_at_ms,omitempty"` // nil → never
}

// Invite is an element of the GET /v1/invites response list.
type Invite struct {
	InviteID    string   `json:"invite_id"`
	TenantID    string   `json:"tenant_id"`
	Name        string   `json:"name"`
	Hostnames   []string `json:"hostnames"`
	Region      string   `json:"region"`
	ExpiresAtMs *int64   `json:"expires_at_ms,omitempty"`
}

// AddHostnamesRequest is the body of POST /v1/invites/{id}/hostnames.
type AddHostnamesRequest struct {
	Hostnames []string `json:"hostnames"`
}

// AddHostnamesResponse is returned by POST /v1/invites/{id}/hostnames.
type AddHostnamesResponse struct {
	Status    string   `json:"status"`
	Hostnames []string `json:"hostnames"`
}

// AvailablePortsResponse is returned by GET /v1/ports/available.
type AvailablePortsResponse struct {
	Protocol   string  `json:"protocol"`
	RangeStart int32   `json:"range_start"`
	RangeEnd   int32   `json:"range_end"`
	Ports      []int32 `json:"ports"`
}

// EdgeInfo describes the edge node a reservation landed on.
type EdgeInfo struct {
	NodeID    string   `json:"node_id"`
	Addresses []string `json:"addresses"`
}

// ReservePortRequest is the body of POST /v1/tenants/{id}/ports.
type ReservePortRequest struct {
	Protocol  string  `json:"protocol"`            // "tcp" or "udp"
	Preferred *int32  `json:"preferred,omitempty"` // pin public port
	IP        *string `json:"ip,omitempty"`
	Label     *string `json:"label,omitempty"`
}

// ReservePortResponse is returned by POST /v1/tenants/{id}/ports and elements
// of GET /v1/tenants/{id}/ports.
type ReservePortResponse struct {
	Status      string    `json:"status"`
	Port        int32     `json:"port"`
	Protocol    string    `json:"protocol"`
	ClaimedAtMs int64     `json:"claimed_at_ms"`
	IP          *string   `json:"ip,omitempty"`
	Label       *string   `json:"label,omitempty"`
	Edge        *EdgeInfo `json:"edge,omitempty"`
}
