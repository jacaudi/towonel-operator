package envtest_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func TestEnvtestRoundTripsCR(t *testing.T) {
	c, err := client.New(sharedCfg, client.Options{Scheme: testScheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "smoke", Namespace: "default"}}
	if err := c.Create(ctx, tt); err != nil {
		t.Fatalf("create: %v", err)
	}
	var got towonelv1alpha1.TowonelTunnel
	if err := c.Get(ctx, types.NamespacedName{Name: "smoke", Namespace: "default"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = c.Delete(ctx, &got)
}
