package controller

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func TestBuildTokenSecret(t *testing.T) {
	sec := buildTokenSecret("app", "net", secret("tok123"), "inv-1")
	if sec.Name != "app-token" || sec.Namespace != "net" {
		t.Fatalf("name/ns = %s/%s", sec.Name, sec.Namespace)
	}
	if string(sec.Data[tokenDataKey]) != "tok123" {
		t.Errorf("token = %q", sec.Data[tokenDataKey])
	}
	if sec.Labels[LabelPartOf] != PartOfValue || sec.Annotations[AnnotationInviteID] != "inv-1" {
		t.Errorf("missing label/annotation: %+v", sec.ObjectMeta)
	}
}

func TestTokenSecretNeedsWrite(t *testing.T) {
	ok := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{AnnotationInviteID: "inv-1"}}, Data: map[string][]byte{tokenDataKey: []byte("tok")}}
	if tokenSecretNeedsWrite(ok, "inv-1") {
		t.Error("up-to-date should not need write")
	}
	if !tokenSecretNeedsWrite(ok, "inv-2") {
		t.Error("invite-id change should need write")
	}
	if !tokenSecretNeedsWrite(nil, "inv-1") {
		t.Error("missing should need write")
	}
}

// ensureTokenSecret: adoption (token=="") with NO existing Secret -> errAdoptedNoToken.
func TestEnsureTokenSecretAdoptNoSecret(t *testing.T) {
	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "net"}}
	tt.Status.InviteID = "inv-1"
	r := &TowonelTunnelReconciler{Client: newFakeClient(t), Scheme: testScheme(t)}
	err := r.ensureTokenSecret(t.Context(), tt, secret(""))
	if !errors.Is(err, errAdoptedNoToken) {
		t.Fatalf("want errAdoptedNoToken, got %v", err)
	}
}

// ensureTokenSecret: fresh token writes the Secret + publishes status.
func TestEnsureTokenSecretWrites(t *testing.T) {
	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "net"}}
	tt.Status.InviteID = "inv-1"
	r := &TowonelTunnelReconciler{Client: newFakeClient(t), Scheme: testScheme(t)}
	if err := r.ensureTokenSecret(t.Context(), tt, secret("tok")); err != nil {
		t.Fatal(err)
	}
	if tt.Status.TokenSecretRef == nil || tt.Status.TokenSecretRef.Name != "app-token" {
		t.Fatalf("tokenSecretRef = %+v", tt.Status.TokenSecretRef)
	}
	var got corev1.Secret
	if err := r.Get(t.Context(), types.NamespacedName{Name: "app-token", Namespace: "net"}, &got); err != nil {
		t.Fatalf("secret not written: %v", err)
	}
	// Adoption now finds a good Secret -> no error, no rewrite needed.
	if err := r.ensureTokenSecret(t.Context(), tt, secret("")); err != nil {
		t.Fatalf("adopt with existing secret should be nil, got %v", err)
	}
}
