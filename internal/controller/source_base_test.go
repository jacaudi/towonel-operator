package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// TestCrossWatchPredicateSkipsStatusOnlyUpdates locks the cross-watch predicate
// used by the TowonelAgent and parent-Gateway watches: it must skip status-only
// updates (churn) while still passing Create (the #22 scenario) and any spec,
// annotation, or label change. The annotation case holds generation constant so
// it proves AnnotationChanged — not generation — lets the event through; this is
// the regression guard for gateway-service (an ANNOTATION that never bumps
// metadata.generation).
func TestCrossWatchPredicateSkipsStatusOnlyUpdates(t *testing.T) {
	p := crossWatchPredicate()

	base := func() *corev1.Service {
		return &corev1.Service{
			Namespace: "net", Name: "gw", Generation: 1, ResourceVersion: "100",
			Annotations: map[string]string{"towonel.io/gateway-service": "net/gw:443"},
			Labels:      map[string]string{"k": "v"}}
	}

	if !p.Create(event.CreateEvent{Object: base()}) {
		t.Fatal("Create must pass (the #22 scenario)")
	}

	genBumped := base()
	genBumped.Generation = 2
	genBumped.ResourceVersion = "101"
	if !p.Update(event.UpdateEvent{ObjectOld: base(), ObjectNew: genBumped}) {
		t.Fatal("generation-changed update must pass")
	}

	annChanged := base()
	annChanged.ResourceVersion = "101" // generation held at 1 on purpose
	annChanged.Annotations = map[string]string{"towonel.io/gateway-service": "net/gw:8443"}
	if !p.Update(event.UpdateEvent{ObjectOld: base(), ObjectNew: annChanged}) {
		t.Fatal("annotation-changed update must pass (gateway-service regression guard)")
	}

	statusOnly := base()
	statusOnly.ResourceVersion = "101" // only resourceVersion differs; gen/ann/labels identical
	if p.Update(event.UpdateEvent{ObjectOld: base(), ObjectNew: statusOnly}) {
		t.Fatal("status-only update must be filtered out")
	}

	// Compile-time assertion that the helper returns a usable predicate type.
	var _ client.Object = base()
}

func TestObserveUserAgentWarnsOnUnservedHostname(t *testing.T) {
	user := &towonelv1alpha1.TowonelAgent{}
	user.Namespace, user.Name = "net", "mine"
	user.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: "net"}
	c := fake.NewClientBuilder().WithScheme(srcScheme(t)).WithObjects(user).Build()

	rec := record.NewFakeRecorder(8)
	b := &sourceBase{}
	b.ensure(rec)
	src := &corev1.Service{Namespace: "net", Name: "svc"}
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

func TestObserveUserAgentActionableMessage(t *testing.T) {
	b := &sourceBase{}
	target := &towonelv1alpha1.TowonelAgent{}
	target.Namespace, target.Name = "net", "mine"
	target.Spec.Mode = towonelv1alpha1.ModeObserveOnly
	target.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: "net"}

	var msgs []string
	emit := func(_, msg string) { msgs = append(msgs, msg) }
	rt := routing{services: []map[string]any{{"hostname": "new.example", "origin": "o:1"}}}

	b.observeUserAgent(emit, target, types.NamespacedName{Namespace: "net", Name: "app"}, rt)

	if len(msgs) != 1 {
		t.Fatalf("want 1 advisory message, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "spec.mode: Managed") {
		t.Fatalf("ObserveOnly message must tell the user how to opt in; got %q", msgs[0])
	}
}

func TestObserveUserAgentWarnsWhenDNSOptOutNotOnAgent(t *testing.T) {
	tests := []struct {
		name      string
		dnsRecord *bool // on the agent's service entry
		srcOptOut bool  // source stamped dnsRecord:false
		want      bool
	}{
		{"agent serves without dnsRecord", nil, true, true},
		{"agent serves with dnsRecord true", new(true), true, true},
		{"agent already opts out", new(false), true, false},
		{"source did not opt out", nil, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := &towonelv1alpha1.TowonelAgent{}
			target.Namespace, target.Name = "net", "mine"
			target.Spec.Mode = towonelv1alpha1.ModeObserveOnly
			target.Spec.TunnelRef = towonelv1alpha1.TunnelReference{Name: "app", Namespace: "net"}
			target.Spec.Services = []towonelv1alpha1.AgentService{{Hostname: "a.example", Origin: "o:1", DNSRecord: tc.dnsRecord}}
			entry := map[string]any{"hostname": "a.example", "origin": "o:1"}
			if tc.srcOptOut {
				entry["dnsRecord"] = false
			}

			var got [][2]string
			emit := func(reason, msg string) { got = append(got, [2]string{reason, msg}) }
			(&sourceBase{}).observeUserAgent(emit, target, types.NamespacedName{Namespace: "net", Name: "app"}, routing{services: []map[string]any{entry}})

			if !tc.want {
				if len(got) != 0 {
					t.Fatalf("events = %v, want none", got)
				}
				return
			}
			want := [2]string{ReasonObserveOnly, `"a.example" is annotated towonel.io/dns-record but agent "mine" serves it without dnsRecord: false; set it on the agent`}
			if len(got) != 1 || got[0] != want {
				t.Fatalf("events = %v, want exactly [%v]", got, want)
			}
		})
	}
}
