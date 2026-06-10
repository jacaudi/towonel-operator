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

// errAdoptedNoToken: an invite was adopted (status only) but its token isn't
// available (CreateInvite returns the token; ListInvites does not) and no token
// Secret exists to reuse — so the Secret cannot be (re)constructed.
var errAdoptedNoToken = errors.New("adopted invite has no stored token; re-create the tunnel or restore the token Secret")

func buildTokenSecret(name, namespace string, token secret, inviteID string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        tokenSecretName(name),
			Namespace:   namespace,
			Labels:      map[string]string{LabelPartOf: PartOfValue},
			Annotations: map[string]string{AnnotationInviteID: inviteID},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{tokenDataKey: []byte(token.Expose())},
	}
}

func tokenSecretNeedsWrite(current *corev1.Secret, inviteID string) bool {
	if current == nil || len(current.Data[tokenDataKey]) == 0 {
		return true
	}
	return current.Annotations[AnnotationInviteID] != inviteID
}

// ensureTokenSecret writes/owns the token Secret idempotently.
//   - token != "" (fresh create): write iff needed.
//   - token == "" (adopt/steady-state): require an existing, current Secret;
//     if absent -> errAdoptedNoToken (caller maps to Ready=False/Reconciling).
func (r *TowonelTunnelReconciler) ensureTokenSecret(ctx context.Context, tt *towonelv1alpha1.TowonelTunnel, token secret) error {
	nn := types.NamespacedName{Namespace: tt.Namespace, Name: tokenSecretName(tt.Name)}
	var current corev1.Secret
	getErr := r.Get(ctx, nn, &current)
	exists := getErr == nil
	if getErr != nil && !apierrors.IsNotFound(getErr) {
		return fmt.Errorf("get token secret %s: %w", nn, getErr)
	}

	if token == "" {
		if exists && !tokenSecretNeedsWrite(&current, tt.Status.InviteID) {
			tt.Status.TokenSecretRef = &towonelv1alpha1.SecretReference{Name: nn.Name, Namespace: nn.Namespace}
			return nil
		}
		return errAdoptedNoToken
	}

	if exists && !tokenSecretNeedsWrite(&current, tt.Status.InviteID) {
		tt.Status.TokenSecretRef = &towonelv1alpha1.SecretReference{Name: nn.Name, Namespace: nn.Namespace}
		return nil
	}

	desired := buildTokenSecret(tt.Name, tt.Namespace, token, tt.Status.InviteID)
	if err := controllerutil.SetControllerReference(tt, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner ref: %w", err)
	}
	if err := r.Patch(ctx, desired, client.Apply, client.FieldOwner(FieldOwner), client.ForceOwnership); err != nil {
		return fmt.Errorf("apply token secret %s: %w", nn, err)
	}
	tt.Status.TokenSecretRef = &towonelv1alpha1.SecretReference{Name: desired.Name, Namespace: desired.Namespace}
	return nil
}
