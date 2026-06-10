package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/towonel"
	"github.com/jacaudi/towonel-operator/internal/towonel/towoneltest"
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

func TestEnsureInviteCreates(t *testing.T) {
	hub := towoneltest.NewHub()
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)
	r := &TowonelTunnelReconciler{}
	tt := &towonelv1alpha1.TowonelTunnel{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "net"},
		Spec:       towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"a.example"}},
	}
	token, err := r.ensureInvite(t.Context(), tc, tt)
	if err != nil {
		t.Fatalf("ensureInvite: %v", err)
	}
	if token == "" {
		t.Error("create should return a non-empty token")
	}
	if tt.Status.InviteID == "" || tt.Status.TenantID == "" {
		t.Fatalf("status not populated: %+v", tt.Status)
	}
	if len(tt.Status.AuthorizedHostnames) != 1 || tt.Status.AuthorizedHostnames[0] != "a.example" {
		t.Errorf("authorizedHostnames not seeded: %v", tt.Status.AuthorizedHostnames)
	}
	token2, err := r.ensureInvite(t.Context(), tc, tt) // status.inviteId set -> no-op
	if err != nil || token2 != "" {
		t.Fatalf("second call: token=%q err=%v (want empty,nil)", token2, err)
	}
	if hub.Created != 1 {
		t.Errorf("created %d, want 1", hub.Created)
	}
}

func TestEnsureInviteAdopts(t *testing.T) {
	hub := towoneltest.NewHub()
	hub.Seed(towonel.Invite{InviteID: "inv-9", TenantID: "ten-9", Name: inviteName("net", "app"), Hostnames: []string{"x.example"}})
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)
	r := &TowonelTunnelReconciler{}
	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "net"}}
	token, err := r.ensureInvite(t.Context(), tc, tt)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		t.Error("adoption must NOT return a token")
	}
	if tt.Status.InviteID != "inv-9" || hub.Created != 0 {
		t.Errorf("adopt failed: id=%q created=%d", tt.Status.InviteID, hub.Created)
	}
	if len(tt.Status.AuthorizedHostnames) != 1 || tt.Status.AuthorizedHostnames[0] != "x.example" {
		t.Errorf("adopt should seed hostnames: %v", tt.Status.AuthorizedHostnames)
	}
}

func TestConvergeHostnames(t *testing.T) {
	hub := towoneltest.NewHub()
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)
	r := &TowonelTunnelReconciler{}
	tt := &towonelv1alpha1.TowonelTunnel{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "net"},
		Spec:       towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"a.example", "b.example"}},
	}
	if _, err := r.ensureInvite(t.Context(), tc, tt); err != nil { // creates with a,b; seeds observed
		t.Fatal(err)
	}
	tt.Spec.ExtraHostnames = []string{"a.example", "c.example"} // drop b, add c
	if err := r.convergeHostnames(t.Context(), tc, tt); err != nil {
		t.Fatalf("converge: %v", err)
	}
	got := map[string]bool{}
	for _, h := range tt.Status.AuthorizedHostnames {
		got[h] = true
	}
	if !got["a.example"] || !got["c.example"] || got["b.example"] {
		t.Errorf("authorized = %v, want {a,c}", tt.Status.AuthorizedHostnames)
	}
}
