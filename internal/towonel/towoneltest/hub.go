// Package towoneltest provides an in-memory fake Towonel hub for tests.
package towoneltest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/jacaudi/towonel-operator/internal/towonel"
)

// Hub is an in-memory Towonel hub: create/list/delete invites + add/remove hostnames.
type Hub struct {
	mu       sync.Mutex
	invites  map[string]*towonel.Invite
	tokens   map[string]string // inviteID -> token
	nextID   int
	Created  int
	ports    map[string][]*towonel.ReservePortResponse // tenantID -> reservations
	taken    map[string]bool                           // "proto/port" -> globally taken
	nextPort int32

	reservedHosts map[string]string // hostname -> owning inviteID
}

func NewHub() *Hub {
	return &Hub{
		invites:  map[string]*towonel.Invite{},
		tokens:   map[string]string{},
		ports:    map[string][]*towonel.ReservePortResponse{},
		taken:    map[string]bool{},
		nextPort: 30000,

		reservedHosts: map[string]string{},
	}
}

// Seed inserts a pre-existing invite (for adoption tests).
func (h *Hub) Seed(inv towonel.Invite) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := inv
	h.invites[inv.InviteID] = &cp
	for _, hn := range cp.Hostnames {
		h.reservedHosts[hn] = cp.InviteID
	}
}

// Has reports whether an invite id is present.
func (h *Hub) Has(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.invites[id]
	return ok
}

// SeedTakenPort marks a port as taken hub-wide (conflicts any preferred reserve).
func (h *Hub) SeedTakenPort(protocol string, port int32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.taken[fmt.Sprintf("%s/%d", protocol, port)] = true
}

// SeedReservation inserts a pre-existing reservation (adoption tests).
// Note: nextPort does not skip seeded ports; tests seeding ports >= 30000 should avoid auto-allocation for the same protocol.
func (h *Hub) SeedReservation(tenantID string, r towonel.ReservePortResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := r
	h.ports[tenantID] = append(h.ports[tenantID], &cp)
	h.taken[fmt.Sprintf("%s/%d", r.Protocol, r.Port)] = true
}

// HasReservation reports whether a tenant holds (protocol, port).
func (h *Hub) HasReservation(tenantID, protocol string, port int32) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.ports[tenantID] {
		if r.Protocol == protocol && r.Port == port {
			return true
		}
	}
	return false
}

// ReservationCount returns a tenant's live reservation count.
func (h *Hub) ReservationCount(tenantID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.ports[tenantID])
}

// Server returns a started httptest.Server and a client pointed at it.
// The caller is responsible for closing the server (e.g. t.Cleanup(srv.Close)).
func (h *Hub) Server() (*httptest.Server, *towonel.Client) {
	srv := httptest.NewServer(h.handler())
	return srv, towonel.NewClient(srv.URL, "twk_test", srv.Client())
}

func (h *Hub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		p := req.URL.Path
		switch {
		case req.Method == http.MethodPost && p == "/v1/invites":
			var body towonel.CreateInviteRequest
			_ = json.NewDecoder(req.Body).Decode(&body)
			h.nextID++
			h.Created++
			id := fmt.Sprintf("inv-%d", h.nextID)
			name := ""
			if body.Name != nil {
				name = *body.Name
			}
			h.invites[id] = &towonel.Invite{InviteID: id, TenantID: "ten-" + id, Name: name, Hostnames: body.Hostnames}
			h.tokens[id] = "tok-" + id
			for _, hn := range body.Hostnames {
				h.reservedHosts[hn] = id
			}
			_ = json.NewEncoder(w).Encode(towonel.CreateInviteResponse{Status: "ok", Token: "tok-" + id, InviteID: id, TenantID: "ten-" + id, Name: name})
		case req.Method == http.MethodGet && p == "/v1/invites":
			out := make([]towonel.Invite, 0, len(h.invites))
			for _, inv := range h.invites {
				out = append(out, *inv)
			}
			_ = json.NewEncoder(w).Encode(out)
		case req.Method == http.MethodPost && strings.HasSuffix(p, "/hostnames"):
			id := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/invites/"), "/hostnames")
			var body towonel.AddHostnamesRequest
			_ = json.NewDecoder(req.Body).Decode(&body)
			inv := h.invites[id]
			if inv == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			for _, hn := range body.Hostnames {
				if _, taken := h.reservedHosts[hn]; taken {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"error":{"code":"hostname_conflict","message":"hostname ` + hn + ` is already reserved by an active invite"}}`))
					return
				}
			}
			for _, hn := range body.Hostnames {
				h.reservedHosts[hn] = id
				inv.Hostnames = append(inv.Hostnames, hn)
			}
			_ = json.NewEncoder(w).Encode(towonel.AddHostnamesResponse{Status: "ok", Hostnames: inv.Hostnames})
		case req.Method == http.MethodDelete && strings.Contains(p, "/hostnames/"):
			parts := strings.SplitN(strings.TrimPrefix(p, "/v1/invites/"), "/hostnames/", 2)
			id, host := parts[0], parts[1]
			if inv := h.invites[id]; inv != nil {
				kept := inv.Hostnames[:0]
				for _, hn := range inv.Hostnames {
					if hn != host {
						kept = append(kept, hn)
					}
				}
				inv.Hostnames = kept
				delete(h.reservedHosts, host)
				_ = json.NewEncoder(w).Encode(towonel.RemoveHostnameResponse{Status: "ok", Hostname: host, RemainingHostnames: inv.Hostnames})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case req.Method == http.MethodDelete && strings.HasPrefix(p, "/v1/invites/"):
			id := strings.TrimPrefix(p, "/v1/invites/")
			inv, ok := h.invites[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			for _, hn := range inv.Hostnames {
				delete(h.reservedHosts, hn)
			}
			delete(h.invites, id)
			w.WriteHeader(http.StatusOK)
		case req.Method == http.MethodPost && strings.HasPrefix(p, "/v1/tenants/") && strings.HasSuffix(p, "/ports"):
			tenant := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/tenants/"), "/ports")
			var body towonel.ReservePortRequest
			_ = json.NewDecoder(req.Body).Decode(&body)
			port := h.nextPort
			if body.Preferred != nil {
				if h.taken[fmt.Sprintf("%s/%d", body.Protocol, *body.Preferred)] {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"error":"port taken"}`))
					return
				}
				port = *body.Preferred
			} else {
				h.nextPort++
			}
			h.taken[fmt.Sprintf("%s/%d", body.Protocol, port)] = true
			resp := &towonel.ReservePortResponse{
				Status: "ok", Port: port, Protocol: body.Protocol, ClaimedAtMs: 1,
				Label: body.Label,
				Edge:  &towonel.EdgeInfo{NodeID: "edge-1", Addresses: []string{"203.0.113.10"}},
			}
			h.ports[tenant] = append(h.ports[tenant], resp)
			_ = json.NewEncoder(w).Encode(resp)
		case req.Method == http.MethodGet && strings.HasPrefix(p, "/v1/tenants/") && strings.HasSuffix(p, "/ports"):
			tenant := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/tenants/"), "/ports")
			out := make([]towonel.ReservePortResponse, 0, len(h.ports[tenant]))
			for _, r := range h.ports[tenant] {
				out = append(out, *r)
			}
			_ = json.NewEncoder(w).Encode(out)
		case req.Method == http.MethodDelete && strings.HasPrefix(p, "/v1/tenants/") && strings.Contains(p, "/ports/"):
			rest := strings.TrimPrefix(p, "/v1/tenants/")
			parts := strings.SplitN(rest, "/ports/", 2) // tenant, "proto/port"
			if len(parts) != 2 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			pp := strings.SplitN(parts[1], "/", 2)
			if len(pp) != 2 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			port64, _ := strconv.ParseInt(pp[1], 10, 32)
			tenant, proto, port := parts[0], pp[0], int32(port64)
			kept := h.ports[tenant][:0]
			found := false
			for _, r := range h.ports[tenant] {
				if r.Protocol == proto && r.Port == port {
					found = true
					delete(h.taken, fmt.Sprintf("%s/%d", proto, port))
					continue
				}
				kept = append(kept, r)
			}
			h.ports[tenant] = kept
			if !found {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}
