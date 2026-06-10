// Package towoneltest provides an in-memory fake Towonel hub for tests.
package towoneltest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/jacaudi/towonel-operator/internal/towonel"
)

// Hub is an in-memory Towonel hub: create/list/delete invites + add/remove hostnames.
type Hub struct {
	mu      sync.Mutex
	invites map[string]*towonel.Invite
	tokens  map[string]string // inviteID -> token
	nextID  int
	Created int
}

func NewHub() *Hub {
	return &Hub{invites: map[string]*towonel.Invite{}, tokens: map[string]string{}}
}

// Seed inserts a pre-existing invite (for adoption tests).
func (h *Hub) Seed(inv towonel.Invite) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := inv
	h.invites[inv.InviteID] = &cp
}

// Has reports whether an invite id is present.
func (h *Hub) Has(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.invites[id]
	return ok
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
			if inv := h.invites[id]; inv != nil {
				inv.Hostnames = append(inv.Hostnames, body.Hostnames...)
				_ = json.NewEncoder(w).Encode(towonel.AddHostnamesResponse{Status: "ok", Hostnames: inv.Hostnames})
				return
			}
			w.WriteHeader(http.StatusNotFound)
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
				_ = json.NewEncoder(w).Encode(towonel.RemoveHostnameResponse{Status: "ok", Hostname: host, RemainingHostnames: inv.Hostnames})
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case req.Method == http.MethodDelete && strings.HasPrefix(p, "/v1/invites/"):
			id := strings.TrimPrefix(p, "/v1/invites/")
			if _, ok := h.invites[id]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(h.invites, id)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}
