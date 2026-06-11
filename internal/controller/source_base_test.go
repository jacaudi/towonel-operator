package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func TestObserveUserAgentWarnsOnUnservedHostname(t *testing.T) {
	user := &towonelv1alpha1.TowonelAgent{}
	user.Namespace, user.Name = "net", "mine"
	user.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: "net"}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(user).Build()

	rec := record.NewFakeRecorder(8)
	b := &sourceBase{}
	b.ensure(rec)
	src := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "net", Name: "svc"}}
	rt := routing{services: []map[string]any{{"hostname": "x.example", "origin": "x:1"}}}
	b.observeUserAgent(func(r, m string) { b.dedupe.emit(b.recorder, src, corev1.EventTypeWarning, r, m) },
		user, types.NamespacedName{Namespace: "net", Name: "app"}, rt)
	select {
	case ev := <-rec.Events:
		if ev == "" {
			t.Fatal("expected an observe-only Event")
		}
	default:
		t.Fatal("expected an Event for an unserved hostname")
	}
	_ = c
}
