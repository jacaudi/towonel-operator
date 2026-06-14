package controller

import (
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
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

	// Missing secret ref → halt (Ready=False/InvalidConfig), NOT a transient error.
	ttMissing := &towonelv1alpha1.TowonelTunnel{
		ObjectMeta: metav1.ObjectMeta{Name: "app2", Namespace: "net"},
		Spec:       towonelv1alpha1.TowonelTunnelSpec{APIKeySecretRef: &towonelv1alpha1.SecretKeyRef{Name: "missing", Key: "token"}},
	}
	rMissing := &TowonelTunnelReconciler{Client: newFakeClient(t)} // no Secret registered
	_, haltMissing, errMissing := rMissing.resolveAPIKey(t.Context(), ttMissing)
	if errMissing != nil || !haltMissing {
		t.Fatalf("missing-secret: halt=%v err=%v (want halt=true, err=nil)", haltMissing, errMissing)
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
	token, err := r.ensureInvite(t.Context(), tc, tt, desiredHostnames(tt, nil))
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
	token2, err := r.ensureInvite(t.Context(), tc, tt, desiredHostnames(tt, nil)) // status.inviteId set -> no-op
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
	token, err := r.ensureInvite(t.Context(), tc, tt, desiredHostnames(tt, nil))
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

func TestEnsureInviteCreatesWithFullDesired(t *testing.T) {
	hub := towoneltest.NewHub()
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)

	r := &TowonelTunnelReconciler{}
	tt := &towonelv1alpha1.TowonelTunnel{
		ObjectMeta: metav1.ObjectMeta{Name: "t1", Namespace: "ns"},
		Spec:       towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"extra.example"}},
	}
	desired := []string{"agent.example", "extra.example"}

	token, err := r.ensureInvite(t.Context(), tc, tt, desired)
	if err != nil {
		t.Fatalf("ensureInvite: %v", err)
	}
	if token == "" {
		t.Fatal("expected a non-empty token on create")
	}
	if tt.Status.InviteID == "" {
		t.Fatal("expected status.InviteID to be set")
	}
	got := tt.Status.AuthorizedHostnames
	if len(got) != 2 || got[0] != "agent.example" || got[1] != "extra.example" {
		t.Fatalf("AuthorizedHostnames = %v, want [agent.example extra.example]", got)
	}
	if !hub.HasHostname(tt.Status.InviteID, "agent.example") {
		t.Fatal("invite must be created with the agent-derived hostname, not just extraHostnames")
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
	if _, err := r.ensureInvite(t.Context(), tc, tt, desiredHostnames(tt, nil)); err != nil { // creates with a,b; seeds observed
		t.Fatal(err)
	}
	tt.Spec.ExtraHostnames = []string{"a.example", "c.example"} // drop b, add c
	if err := r.convergeHostnames(t.Context(), tc, tt, desiredHostnames(tt, nil)); err != nil {
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

func TestConvergeHostnamesAbsorbsOwnConflict(t *testing.T) {
	hub := towoneltest.NewHub()
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)

	// An invite already authorizes the hostname (its hostname is reserved hub-side).
	hub.Seed(towonel.Invite{InviteID: "inv-1", TenantID: "ten-1", Name: "ns/t1", Hostnames: []string{"a.example"}})

	r := &TowonelTunnelReconciler{Recorder: record.NewFakeRecorder(8)}
	tt := &towonelv1alpha1.TowonelTunnel{
		ObjectMeta: metav1.ObjectMeta{Name: "t1", Namespace: "ns"},
	}
	tt.Status.InviteID = "inv-1"
	tt.Status.AuthorizedHostnames = nil // STALE: status lost the hostname (the bug condition)

	// Must NOT return an error (the 409 is our own hostname → idempotent success).
	if err := r.convergeHostnames(t.Context(), tc, tt, []string{"a.example"}); err != nil {
		t.Fatalf("convergeHostnames returned error on own-invite conflict: %v", err)
	}
	if got := tt.Status.AuthorizedHostnames; len(got) != 1 || got[0] != "a.example" {
		t.Fatalf("AuthorizedHostnames = %v, want [a.example]", got)
	}
}

func TestConvergeHostnamesAddsNewHostname(t *testing.T) {
	hub := towoneltest.NewHub()
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)
	hub.Seed(towonel.Invite{InviteID: "inv-1", TenantID: "ten-1", Name: "ns/t1", Hostnames: []string{"a.example"}})

	r := &TowonelTunnelReconciler{Recorder: record.NewFakeRecorder(8)}
	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "t1", Namespace: "ns"}}
	tt.Status.InviteID = "inv-1"
	tt.Status.AuthorizedHostnames = []string{"a.example"}

	if err := r.convergeHostnames(t.Context(), tc, tt, []string{"a.example", "b.example"}); err != nil {
		t.Fatalf("convergeHostnames: %v", err)
	}
	if got := tt.Status.AuthorizedHostnames; len(got) != 2 || got[0] != "a.example" || got[1] != "b.example" {
		t.Fatalf("AuthorizedHostnames = %v, want [a.example b.example]", got)
	}
}

func TestConvergeHostnamesPropagatesNon409(t *testing.T) {
	hub := towoneltest.NewHub()
	srv, tc := hub.Server()
	t.Cleanup(srv.Close)
	// No invite seeded: the hub's add handler 404s for an unknown invite ID.
	// A 404 is NOT an absorbable own-409 hostname_conflict — it must propagate.

	r := &TowonelTunnelReconciler{Recorder: record.NewFakeRecorder(8)}
	tt := &towonelv1alpha1.TowonelTunnel{ObjectMeta: metav1.ObjectMeta{Name: "t1", Namespace: "ns"}}
	tt.Status.InviteID = "inv-missing"  // hub has no such invite → add returns 404
	tt.Status.AuthorizedHostnames = nil // observed empty → the add is attempted

	err := r.convergeHostnames(t.Context(), tc, tt, []string{"new.example"})
	if err == nil {
		t.Fatal("convergeHostnames absorbed a non-409 error; want it propagated")
	}
	if slices.Contains(tt.Status.AuthorizedHostnames, "new.example") {
		t.Fatalf("un-added hostname leaked into status: %v", tt.Status.AuthorizedHostnames)
	}
}

func agentWithRoutes(ns, name string, hostnames []string, tcpHostname string) towonelv1alpha1.TowonelAgent {
	ta := towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
	}
	for _, h := range hostnames {
		ta.Spec.Services = append(ta.Spec.Services, towonelv1alpha1.AgentService{Hostname: h, Origin: "svc:80"})
	}
	if tcpHostname != "" {
		ta.Spec.TCP = append(ta.Spec.TCP, towonelv1alpha1.AgentL4Service{Name: "raw", Origin: "svc:22", Hostname: tcpHostname})
	}
	return ta
}

func TestDesiredHostnames(t *testing.T) {
	tt := &towonelv1alpha1.TowonelTunnel{
		Spec: towonelv1alpha1.TowonelTunnelSpec{ExtraHostnames: []string{"extra.example"}},
	}
	agents := []towonelv1alpha1.TowonelAgent{
		agentWithRoutes("a", "one", []string{"app.example", "shared.example"}, "ssh.example"),
		agentWithRoutes("b", "two", []string{"shared.example"}, ""),
	}
	got := desiredHostnames(tt, agents)
	want := []string{"app.example", "extra.example", "shared.example"} // sorted, deduped, NO tcp hostname
	if !slices.Equal(got, want) {
		t.Errorf("desiredHostnames = %v, want %v", got, want)
	}

	// nil agents → only extraHostnames
	got0 := desiredHostnames(tt, nil)
	if !slices.Equal(got0, []string{"extra.example"}) {
		t.Errorf("nil agents: got %v", got0)
	}
}
