package controller

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// agentScheme includes clientgoscheme (corev1 Secrets, among much else) on
// top of the CRD types. testScheme in this package registers only corev1 +
// CRDs; the two are interchangeable for these tests' needs, not in general.
func agentScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := towonelv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func tunnelWithToken(inviteID, secretInviteID, token string) (*towonelv1alpha1.TowonelTunnel, *corev1.Secret) {
	tt := &towonelv1alpha1.TowonelTunnel{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "network"},
		Status: towonelv1alpha1.TowonelTunnelStatus{
			InviteID:       inviteID,
			TokenSecretRef: &towonelv1alpha1.SecretReference{Name: "app-token", Namespace: "network"},
		},
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app-token", Namespace: "network",
			Annotations: map[string]string{AnnotationInviteID: secretInviteID},
		},
		Data: map[string][]byte{tokenDataKey: []byte(token)},
	}
	return tt, sec
}

func TestReadTunnelTokenGates(t *testing.T) {
	ta := &towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-a", Namespace: "selfhosted"},
		Spec:       towonelv1alpha1.TowonelAgentSpec{TunnelRef: towonelv1alpha1.TunnelReference{Name: "app", Namespace: "network"}},
	}
	tests := []struct {
		name       string
		objs       func() []client.Object
		wantReason string // "" = ready
	}{
		{"tunnel missing", func() []client.Object { return nil }, ReasonTunnelNotFound},
		{"secret ref unset", func() []client.Object {
			tt, _ := tunnelWithToken("inv-1", "inv-1", "tok")
			tt.Status.TokenSecretRef = nil
			return []client.Object{tt}
		}, ReasonTokenSecretMissing},
		{"secret missing", func() []client.Object {
			tt, _ := tunnelWithToken("inv-1", "inv-1", "tok")
			return []client.Object{tt}
		}, ReasonTokenSecretMissing},
		{"empty token", func() []client.Object {
			tt, sec := tunnelWithToken("inv-1", "inv-1", "")
			return []client.Object{tt, sec}
		}, ReasonTokenSecretMissing},
		{"stale invite-id", func() []client.Object {
			tt, sec := tunnelWithToken("inv-NEW", "inv-OLD", "tok")
			return []client.Object{tt, sec}
		}, ReasonTokenStale},
		{"ready", func() []client.Object {
			tt, sec := tunnelWithToken("inv-1", "inv-1", "tok")
			return []client.Object{tt, sec}
		}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs := tc.objs()
			builder := fake.NewClientBuilder().WithScheme(agentScheme(t))
			if len(objs) > 0 {
				builder = builder.WithObjects(objs...)
			}
			c := builder.Build()
			r := &TowonelAgentReconciler{Client: c, Scheme: agentScheme(t)}
			_, token, gate, err := r.readTunnelToken(t.Context(), ta)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantReason == "" {
				if gate != nil {
					t.Fatalf("want ready, got gate %+v", gate)
				}
				if token.Expose() != "tok" {
					t.Fatalf("token = %q", token.Expose())
				}
				return
			}
			if gate == nil || gate.reason != tc.wantReason {
				t.Fatalf("gate = %+v, want reason %s", gate, tc.wantReason)
			}
		})
	}
}

func TestEnsureAgentSecretClashGuard(t *testing.T) {
	s := agentScheme(t)
	ta := &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "edge-a", Namespace: "selfhosted", UID: "uid-agent"}}
	foreign := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: "edge-a-token", Namespace: "selfhosted",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: "towonel.io/v1alpha1", Kind: "TowonelTunnel", Name: "edge-a", UID: "uid-other", Controller: new(true),
		}},
	}}
	unowned := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: "edge-a-token", Namespace: "other",
	}}
	taOther := &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "edge-a", Namespace: "other", UID: "uid-agent2"}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(ta, foreign, unowned, taOther).Build()
	r := &TowonelAgentReconciler{Client: c, Scheme: s}
	if err := r.ensureAgentSecret(t.Context(), ta, secret("tok"), "inv-1"); !errors.Is(err, errSecretClash) {
		t.Fatalf("foreign-owned: want errSecretClash, got %v", err)
	}
	if err := r.ensureAgentSecret(t.Context(), taOther, secret("tok"), "inv-1"); !errors.Is(err, errSecretClash) {
		t.Fatalf("un-owned: want errSecretClash, got %v", err)
	}
}

func TestEnsureAgentSecretWriteAndSkip(t *testing.T) {
	s := agentScheme(t)
	ta := &towonelv1alpha1.TowonelAgent{ObjectMeta: metav1.ObjectMeta{Name: "edge-a", Namespace: "selfhosted", UID: "uid-agent"}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(ta).Build()
	r := &TowonelAgentReconciler{Client: c, Scheme: s}
	// Create path.
	if err := r.ensureAgentSecret(t.Context(), ta, secret("tok"), "inv-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if ta.Status.RenderedSecretRef == nil || ta.Status.RenderedSecretRef.Name != "edge-a-token" {
		t.Fatalf("renderedSecretRef = %+v", ta.Status.RenderedSecretRef)
	}
	var sec corev1.Secret
	if err := c.Get(t.Context(), types.NamespacedName{Namespace: "selfhosted", Name: "edge-a-token"}, &sec); err != nil {
		t.Fatal(err)
	}
	if string(sec.Data[tokenDataKey]) != "tok" || sec.Annotations[AnnotationInviteID] != "inv-1" {
		t.Fatalf("secret content: data=%q ann=%q", sec.Data[tokenDataKey], sec.Annotations[AnnotationInviteID])
	}
	rvAfterCreate := sec.ResourceVersion
	// Idempotent skip path: same invite-id -> no write.
	if err := r.ensureAgentSecret(t.Context(), ta, secret("tok"), "inv-1"); err != nil {
		t.Fatalf("skip: %v", err)
	}
	if err := c.Get(t.Context(), types.NamespacedName{Namespace: "selfhosted", Name: "edge-a-token"}, &sec); err != nil {
		t.Fatal(err)
	}
	if sec.ResourceVersion != rvAfterCreate {
		t.Fatal("idempotent pass must not rewrite the secret")
	}
}
