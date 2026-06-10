package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := towonelv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	_ = towonelv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func TestResolveAPIKey(t *testing.T) {
	tt := &towonelv1alpha1.TowonelTunnel{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "net"},
		Spec:       towonelv1alpha1.TowonelTunnelSpec{APIKeySecretRef: &towonelv1alpha1.SecretKeyRef{Name: "tw", Key: "token"}},
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tw", Namespace: "net"},
		Data:       map[string][]byte{"token": []byte("twk_fromref")},
	}
	r := &TowonelTunnelReconciler{Client: newFakeClient(t, sec)}
	key, halt, err := r.resolveAPIKey(t.Context(), tt)
	if err != nil || halt || key.Expose() != "twk_fromref" {
		t.Fatalf("ref path: key=%q halt=%v err=%v", key.Expose(), halt, err)
	}

	t.Setenv("TOWONEL_API_KEY", "twk_fromenv")
	tt2 := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "n"}}
	r2 := &TowonelTunnelReconciler{Client: newFakeClient(t)}
	key2, halt2, err2 := r2.resolveAPIKey(t.Context(), tt2)
	if err2 != nil || halt2 || key2.Expose() != "twk_fromenv" {
		t.Fatalf("env path: key=%q halt=%v err=%v", key2.Expose(), halt2, err2)
	}

	t.Setenv("TOWONEL_API_KEY", "")
	_, halt3, err3 := r2.resolveAPIKey(t.Context(), tt2)
	if err3 != nil || !halt3 {
		t.Fatalf("no-creds: halt=%v err=%v (want halt,no err)", halt3, err3)
	}
}
