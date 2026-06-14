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

// convergeHostnames reconciles authorized hostnames to the precomputed desired set.
// desired must be pre-normalized (sorted, deduped) — use desiredHostnames to compute it.
// status.authorizedHostnames is published from the API response bodies.
func (r *TowonelTunnelReconciler) convergeHostnames(ctx context.Context, tc *towonel.Client, tt *towonelv1alpha1.TowonelTunnel, desired []string) error {
	observed := dedupe(tt.Status.AuthorizedHostnames)
	cur := observed

	for _, h := range desired {
		if slices.Contains(observed, h) {
			continue
		}
		resp, err := tc.AddHostnames(ctx, tt.Status.InviteID, []string{h})
		if err != nil {
			// #14(b): a 409 hostname_conflict for a hostname we WANT authorized means
			// it is already reserved on our invite — desired state already achieved.
			// Absorb as idempotent success (single-tenant: a foreign conflict is
			// indistinguishable by message and is masked — accepted for alpha).
			if apiErr, ok := errors.AsType[*towonel.APIError](err); ok &&
				apiErr.StatusCode == http.StatusConflict && strings.Contains(apiErr.Body, "hostname_conflict") {
				if r.Recorder != nil {
					r.Recorder.Event(tt, corev1.EventTypeWarning, ReasonHostnameConflict,
						fmt.Sprintf("hostname %q already reserved by an active invite; treating as authorized", h))
				}
				if !slices.Contains(cur, h) {
					cur = append(cur, h)
				}
				continue
			}
			return fmt.Errorf("add hostnames: %w", err)
		}
		cur = resp.Hostnames
	}
	for _, h := range observed {
		if !slices.Contains(desired, h) {
			resp, err := tc.RemoveHostname(ctx, tt.Status.InviteID, h)
			if err != nil {
				return fmt.Errorf("remove hostname %q: %w", h, err)
			}
			cur = resp.RemainingHostnames
		}
	}
	tt.Status.AuthorizedHostnames = dedupe(cur)
	return nil
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
