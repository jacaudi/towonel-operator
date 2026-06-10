package controller

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
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
