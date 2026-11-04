package envtest_test

import (
	"testing"

	"k8s.io/apimachinery/pkg/types"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func TestEnvtestRoundTripsCR(t *testing.T) {
	ctx := t.Context()
	tt := &towonelv1alpha1.TowonelTunnel{Name: "smoke", Namespace: "default"}
	if err := k8sClient.Create(ctx, tt); err != nil {
		t.Fatalf("create: %v", err)
	}
	var got towonelv1alpha1.TowonelTunnel
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "smoke", Namespace: "default"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = k8sClient.Delete(ctx, &got)
}
