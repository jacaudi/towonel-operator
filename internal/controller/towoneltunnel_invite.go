package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/towonel"
)

// resolveAPIKey returns the Towonel API key. Precedence: spec.apiKeySecretRef ->
// TOWONEL_API_KEY env. The bool is "halt": no usable credential (caller sets
// Ready=False/InvalidConfig and requeues).
func (r *TowonelTunnelReconciler) resolveAPIKey(ctx context.Context, tt *towonelv1alpha1.TowonelTunnel) (secret, bool, error) {
	if ref := tt.Spec.APIKeySecretRef; ref != nil && ref.Name != "" {
		var s corev1.Secret
		nn := types.NamespacedName{Namespace: tt.Namespace, Name: ref.Name}
		if err := r.Get(ctx, nn, &s); err != nil {
			if apierrors.IsNotFound(err) {
				return "", true, nil // misconfigured ref -> halt (Ready=False/InvalidConfig)
			}
			return "", false, fmt.Errorf("get api-key secret %s: %w", nn, err)
		}
		key := ref.Key
		if key == "" {
			key = defaultTokenKey
		}
		if v := s.Data[key]; len(v) > 0 {
			return secret(v), false, nil
		}
		return "", true, nil
	}
	if v := os.Getenv("TOWONEL_API_KEY"); v != "" {
		return secret(v), false, nil
	}
	return "", true, nil
}

// inviteName is the deterministic, unique invite label for a tunnel.
func inviteName(namespace, name string) string { return namespace + "/" + name }

// ensureInvite makes status.inviteId authoritative. It returns the invite token,
// which is non-empty ONLY on a fresh create (CreateInvite returns the token;
// ListInvites/adoption does not).
func (r *TowonelTunnelReconciler) ensureInvite(ctx context.Context, tc *towonel.Client, tt *towonelv1alpha1.TowonelTunnel, desired []string) (secret, error) {
	if tt.Status.InviteID != "" {
		return "", nil // authority; nothing to do
	}
	want := inviteName(tt.Namespace, tt.Name)

	// Best-effort adoption (list shape unverified; fall through on any doubt).
	if invites, err := tc.ListInvites(ctx); err != nil {
		logf.FromContext(ctx).V(1).Info("list invites failed; will create", "err", err.Error())
	} else {
		for i := range invites {
			if invites[i].Name == want && invites[i].InviteID != "" {
				tt.Status.InviteID = invites[i].InviteID
				tt.Status.TenantID = invites[i].TenantID
				if invites[i].ExpiresAtMs != nil {
					tt.Status.ExpiresAt = *invites[i].ExpiresAtMs
				}
				tt.Status.AuthorizedHostnames = dedupe(invites[i].Hostnames)
				return "", nil
			}
		}
	}

	req := towonel.CreateInviteRequest{Hostnames: dedupe(desired), Name: &want}
	if tt.Spec.Region != "" {
		req.Region = &tt.Spec.Region
	}
	req.FailoverRegions = tt.Spec.FailoverRegions
	if tt.Spec.TokenExpiry != 0 {
		req.ExpiresInSecs = new(tt.Spec.TokenExpiry)
	}
	resp, err := tc.CreateInvite(ctx, req)
	if err != nil {
		return "", fmt.Errorf("create invite: %w", err)
	}
	tt.Status.InviteID = resp.InviteID
	tt.Status.TenantID = resp.TenantID
	if resp.ExpiresAtMs != nil {
		tt.Status.ExpiresAt = *resp.ExpiresAtMs
	}
	tt.Status.AuthorizedHostnames = dedupe(req.Hostnames) // seed observed = initial set (avoids re-add)
	return secret(resp.Token), nil
}

// desiredHostnames unions spec.extraHostnames with the HTTPS hostnames of all
// referencing agents. tcp/udp hostnames are EXCLUDED — they are DNS intent
// only, never SNI authorization (design §4.A; parent §5.2).
func desiredHostnames(tt *towonelv1alpha1.TowonelTunnel, agents []towonelv1alpha1.TowonelAgent) []string {
	hs := slices.Clone(tt.Spec.ExtraHostnames)
	for i := range agents {
		for _, svc := range agents[i].Spec.Services {
			hs = append(hs, svc.Hostname)
		}
	}
	return dedupe(hs)
}

// errInviteNotFound reports that the hub no longer has this invite (deleted
// hub-side): GET /v1/invites/{id} answered 404.
var errInviteNotFound = errors.New("invite not found on hub")

// inviteView is the hub-confirmed state DNS publishes from (design §6.2).
type inviteView struct {
	// Confirmed is the hub GET set plus successful adds minus successful
	// removes. Hostnames absorbed from a 409 hostname_conflict are EXCLUDED:
	// the hub may be routing them to another tenant (gate B3).
	Confirmed []string
	// Region is the invite's hub region, upper-cased; a null hub region is
	// hubDefaultRegion. DNS keys its edge target on this, never spec.region,
	// which is used only at invite creation (gate 2 B3).
	Region string
	// Revoked reports hub invite status "revoked": its hostnames no longer
	// reserve anything hub-side, so nothing may be published for them.
	Revoked bool
}

const (
	hubDefaultRegion    = "EU"      // towonel_common::DEFAULT_REGION (hub v1.13.4)
	inviteStatusRevoked = "revoked" // hub InviteStatus, serialized lowercase
)

// hubRegion normalizes the hub's invite region (null → hubDefaultRegion).
func hubRegion(r string) string {
	if r = strings.ToUpper(strings.TrimSpace(r)); r != "" {
		return r
	}
	return hubDefaultRegion
}

// convergeHostnames reconciles authorized hostnames to the precomputed desired set.
// desired must be pre-normalized (sorted, deduped) — use desiredHostnames to compute it.
//
// The observed set is sourced from HUB TRUTH (GET /v1/invites/{id}), never from
// status.authorizedHostnames: the operator's own record can be stale or corrupted
// (issue #26 ③), and AddHostnames echoes back only the just-added hostnames — not
// the invite's cumulative set — so publishing it would strand previously-authorized
// hostnames. status.authorizedHostnames is recomputed from the hub set plus the
// mutations this call applies. The returned inviteView is what DNS may publish.
func (r *TowonelTunnelReconciler) convergeHostnames(ctx context.Context, tc *towonel.Client, tt *towonelv1alpha1.TowonelTunnel, desired []string) (inviteView, error) {
	invite, err := tc.GetInvite(ctx, tt.Status.InviteID)
	if err != nil {
		// Only a 404 on the invite itself means the hub deleted it; a 404 from
		// the add/remove calls below does not (e.g. another writer already removed
		// the hostname).
		if apiErr, ok := errors.AsType[*towonel.APIError](err); ok && apiErr.StatusCode == http.StatusNotFound {
			return inviteView{}, fmt.Errorf("get invite %q: %w: %w", tt.Status.InviteID, errInviteNotFound, err)
		}
		return inviteView{}, fmt.Errorf("get invite %q: %w", tt.Status.InviteID, err)
	}
	hubSet := dedupe(invite.Hostnames)
	view := inviteView{Region: hubRegion(invite.Region), Revoked: invite.Status == inviteStatusRevoked}
	if view.Revoked {
		// Mutating a revoked invite is pointless; report hub truth, confirm nothing.
		tt.Status.AuthorizedHostnames = hubSet
		return view, nil
	}

	// authorized starts from hub truth and applies the adds/removes this call
	// performs, so status stays faithful to the hub regardless of what status
	// previously (mis)recorded. It also keeps 409-absorbed hostnames, which the hub
	// does NOT hold on this invite. confirmed is hub truth plus successful adds
	// only, minus removes: the set DNS may publish.
	authorized := slices.Clone(hubSet)
	confirmed := slices.Clone(hubSet)

	for _, h := range desired {
		if slices.Contains(hubSet, h) {
			continue
		}
		if _, err := tc.AddHostnames(ctx, tt.Status.InviteID, []string{h}); err != nil {
			// #14(b): a 409 hostname_conflict for a hostname we WANT authorized, but
			// that hub truth (GET above) does not list on our invite, means it is
			// already reserved by ANOTHER invite. The hub will never add it here, so
			// retrying cannot succeed: absorb it rather than fail the reconcile, and
			// keep it in status.authorizedHostnames for compatibility (single-tenant:
			// the conflict is indistinguishable from a foreign claim by message —
			// accepted for alpha). It is NOT confirmed for DNS, so it is never
			// published (design §6.2).
			if apiErr, ok := errors.AsType[*towonel.APIError](err); ok &&
				apiErr.StatusCode == http.StatusConflict && strings.Contains(apiErr.Body, "hostname_conflict") {
				if r.Recorder != nil {
					r.Recorder.Event(tt, corev1.EventTypeWarning, ReasonHostnameConflict,
						fmt.Sprintf("hostname %q is already reserved by another invite; kept in status.authorizedHostnames but not DNS-confirmed", h))
				}
				if !slices.Contains(authorized, h) {
					authorized = append(authorized, h)
				}
				continue
			}
			return inviteView{}, fmt.Errorf("add hostnames: %w", err)
		}
		if !slices.Contains(authorized, h) {
			authorized = append(authorized, h)
		}
		if !slices.Contains(confirmed, h) {
			confirmed = append(confirmed, h)
		}
	}
	for _, h := range hubSet {
		if !slices.Contains(desired, h) {
			if _, err := tc.RemoveHostname(ctx, tt.Status.InviteID, h); err != nil {
				return inviteView{}, fmt.Errorf("remove hostname %q: %w", h, err)
			}
			authorized = slices.DeleteFunc(authorized, func(x string) bool { return x == h })
			confirmed = slices.DeleteFunc(confirmed, func(x string) bool { return x == h })
		}
	}
	tt.Status.AuthorizedHostnames = dedupe(authorized)
	view.Confirmed = dedupe(confirmed)
	return view, nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}
