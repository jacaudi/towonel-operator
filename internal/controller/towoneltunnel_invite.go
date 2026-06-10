package controller

import (
	"context"
	"fmt"
	"os"
	"slices"

	corev1 "k8s.io/api/core/v1"
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
func (r *TowonelTunnelReconciler) ensureInvite(ctx context.Context, tc *towonel.Client, tt *towonelv1alpha1.TowonelTunnel) (secret, error) {
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

	req := towonel.CreateInviteRequest{Hostnames: dedupe(tt.Spec.ExtraHostnames), Name: &want}
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

// convergeHostnames reconciles authorized hostnames to spec.extraHostnames.
// status.authorizedHostnames is published from the API response bodies.
func (r *TowonelTunnelReconciler) convergeHostnames(ctx context.Context, tc *towonel.Client, tt *towonelv1alpha1.TowonelTunnel) error {
	desired := dedupe(tt.Spec.ExtraHostnames)
	observed := dedupe(tt.Status.AuthorizedHostnames)
	cur := observed

	var toAdd []string
	for _, h := range desired {
		if !slices.Contains(observed, h) {
			toAdd = append(toAdd, h)
		}
	}
	if len(toAdd) > 0 {
		resp, err := tc.AddHostnames(ctx, tt.Status.InviteID, toAdd)
		if err != nil {
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
