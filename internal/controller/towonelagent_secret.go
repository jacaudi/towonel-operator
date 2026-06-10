package controller

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// errSecretClash: the agent's token-Secret name is held by a foreign or
// missing controller owner (e.g. a tunnel named like the agent in this
// namespace, or a user-created Secret). Never steal it.
var errSecretClash = errors.New("token secret exists with a foreign or missing controller owner; rename the agent or the clashing object")

// tunnelGate explains why the agent is WaitingForTunnel.
type tunnelGate struct {
	reason  string
	message string
}

// readTunnelToken gates on ARTIFACTS, not conditions (design §4.C): tunnel
// exists, tokenSecretRef set, Secret readable with a non-empty token, and the
// Secret's invite-id annotation matches status.inviteId (rotation consistency).
// Returns (tunnel, token, nil, nil) on success; (nil, "", gate, nil) when
// semantically not-ready; (nil, "", nil, err) for transient faults.
func (r *TowonelAgentReconciler) readTunnelToken(ctx context.Context, ta *towonelv1alpha1.TowonelAgent) (*towonelv1alpha1.TowonelTunnel, secret, *tunnelGate, error) {
	nn := resolvedTunnelRef(ta)
	var tt towonelv1alpha1.TowonelTunnel
	if err := r.Get(ctx, nn, &tt); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, "", &tunnelGate{ReasonTunnelNotFound, fmt.Sprintf("tunnel %s not found", nn)}, nil
		}
		return nil, "", nil, fmt.Errorf("get tunnel %s: %w", nn, err)
	}
	ref := tt.Status.TokenSecretRef
	if ref == nil || ref.Name == "" {
		return nil, "", &tunnelGate{ReasonTokenSecretMissing, fmt.Sprintf("tunnel %s has no token secret yet", nn)}, nil
	}
	secNS := ref.Namespace
	if secNS == "" {
		secNS = tt.Namespace
	}
	var sec corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: secNS, Name: ref.Name}, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, "", &tunnelGate{ReasonTokenSecretMissing, fmt.Sprintf("token secret %s/%s not found", secNS, ref.Name)}, nil
		}
		return nil, "", nil, fmt.Errorf("get token secret %s/%s: %w", secNS, ref.Name, err)
	}
	tok := sec.Data[tokenDataKey]
	if len(tok) == 0 {
		return nil, "", &tunnelGate{ReasonTokenSecretMissing, fmt.Sprintf("token secret %s/%s has no token", secNS, ref.Name)}, nil
	}
	if sec.Annotations[AnnotationInviteID] != tt.Status.InviteID {
		return nil, "", &tunnelGate{ReasonTokenStale, "token secret invite-id lags tunnel status (rotation in flight)"}, nil
	}
	return &tt, secret(tok), nil, nil
}

// ensureAgentSecret copies the tunnel token into the agent's namespace
// (no cross-namespace mounting; api-key never enters this path).
// Reuses buildTokenSecret/tokenSecretNeedsWrite — same operator convention.
func (r *TowonelAgentReconciler) ensureAgentSecret(ctx context.Context, ta *towonelv1alpha1.TowonelAgent, token secret, inviteID string) error {
	nn := types.NamespacedName{Namespace: ta.Namespace, Name: tokenSecretName(ta.Name)}
	var current corev1.Secret
	getErr := r.Get(ctx, nn, &current)
	exists := getErr == nil
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return fmt.Errorf("get agent token secret %s: %w", nn, getErr)
	}
	if exists {
		// Foreign controller owner OR un-owned (user-created) -> never steal.
		// Our own prior writes always carry the controller ref, so owner==nil
		// can only mean someone else's Secret.
		if owner := metav1.GetControllerOf(&current); owner == nil || owner.UID != ta.UID {
			return errSecretClash
		}
		if !tokenSecretNeedsWrite(&current, inviteID) {
			ta.Status.RenderedSecretRef = &towonelv1alpha1.SecretReference{Name: nn.Name, Namespace: nn.Namespace}
			return nil
		}
	}
	desired := buildTokenSecret(ta.Name, ta.Namespace, token, inviteID)
	if err := controllerutil.SetControllerReference(ta, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner ref: %w", err)
	}
	if err := r.Patch(ctx, desired, client.Apply, client.FieldOwner(FieldOwner), client.ForceOwnership); err != nil {
		return fmt.Errorf("apply agent token secret %s: %w", nn, err)
	}
	ta.Status.RenderedSecretRef = &towonelv1alpha1.SecretReference{Name: desired.Name, Namespace: desired.Namespace}
	return nil
}
