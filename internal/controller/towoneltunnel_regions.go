package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/towonel"
)

// populateRegionEndpoints fills status.regionEndpoints from the hub's
// GET /v1/invites/{id}/status (#38; design §7). Best-effort and non-blocking:
// it never fails the reconcile. A 404 (hub too old, or invite unknown) leaves
// regionEndpoints unchanged; any other error keeps the last-known value. Hub
// region order (primary first, then failover) is preserved.
func (r *TowonelTunnelReconciler) populateRegionEndpoints(ctx context.Context, tc *towonel.Client, tt *towonelv1alpha1.TowonelTunnel) {
	if tt.Status.InviteID == "" {
		return
	}
	st, err := hubCall(ctx, func(c context.Context) (*towonel.InviteStatus, error) {
		return tc.GetInviteStatus(c, tt.Status.InviteID)
	})
	if err != nil {
		if apiErr, ok := errors.AsType[*towonel.APIError](err); ok && apiErr.StatusCode == http.StatusNotFound {
			setCond(tt, CondRegionEndpointsAvailable, metav1.ConditionFalse, ReasonUnavailable, "hub returned 404 for /v1/invites/{id}/status (hub too old, or invite unknown)")
			logf.FromContext(ctx).V(1).Info("region status endpoint unavailable", "invite", tt.Status.InviteID)
			return
		}
		setCond(tt, CondRegionEndpointsAvailable, metav1.ConditionFalse, ReasonAPIError, err.Error())
		logf.FromContext(ctx).Info("region status fetch failed; keeping last-known regionEndpoints", "err", err.Error())
		return
	}
	eps := make([]towonelv1alpha1.RegionEndpoint, 0, len(st.Regions))
	live := 0
	for _, rs := range st.Regions {
		var ips []string
		for _, e := range rs.Edges {
			ips = append(ips, e.PublicIPs...)
		}
		ep := towonelv1alpha1.RegionEndpoint{Region: rs.Region, Role: mapRole(rs.Role)}
		if addrs := dedupe(ips); len(addrs) > 0 {
			ep.Addresses = addrs
			live++
		}
		eps = append(eps, ep)
	}
	tt.Status.RegionEndpoints = eps
	setCond(tt, CondRegionEndpointsAvailable, metav1.ConditionTrue, ReasonSynced,
		fmt.Sprintf("%d region(s), %d with live edge(s)", len(eps), live))
}

// mapRole translates the hub's region role ("failover" → "secondary");
// anything else passes through verbatim so a future hub role never fails the
// status write.
func mapRole(hubRole string) string {
	if hubRole == "failover" {
		return "secondary"
	}
	return hubRole
}
