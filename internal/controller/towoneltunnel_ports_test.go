package controller

import (
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

func l4Agent(ns, name string, tcp, udp []towonelv1alpha1.AgentL4Service) towonelv1alpha1.TowonelAgent {
	return towonelv1alpha1.TowonelAgent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       towonelv1alpha1.TowonelAgentSpec{TCP: tcp, UDP: udp},
	}
}

func TestDesiredPorts(t *testing.T) {
	agents := []towonelv1alpha1.TowonelAgent{
		l4Agent("a", "one",
			[]towonelv1alpha1.AgentL4Service{
				{Name: "ssh", Origin: "a:22", PreferredPort: 2222},
				{Name: "game", Origin: "a:4086"},
			},
			[]towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "a:51820"}}, // same name, udp = distinct service
		),
		l4Agent("b", "two",
			[]towonelv1alpha1.AgentL4Service{
				{Name: "ssh", Origin: "b:22", PreferredPort: 2223}, // conflicts with a/one's 2222
				{Name: "game", Origin: "b:4086", PreferredPort: 4086},
			},
			nil,
		),
		// agrees with a/one's winning 2222 — agreement is not a conflict
		l4Agent("c", "three",
			[]towonelv1alpha1.AgentL4Service{{Name: "ssh", Origin: "c:22", PreferredPort: 2222}},
			nil,
		),
	}
	desired, conflicts := desiredPorts(agents)

	keys := make([]string, 0, len(desired))
	for _, d := range desired {
		keys = append(keys, d.protocol+"/"+d.name)
	}
	slices.Sort(keys)
	if want := []string{"tcp/game", "tcp/ssh", "udp/ssh"}; !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for _, d := range desired {
		switch d.protocol + "/" + d.name {
		case "tcp/ssh":
			if d.preferred != 2222 { // first in ns/name order wins
				t.Errorf("tcp/ssh preferred = %d, want 2222", d.preferred)
			}
		case "tcp/game":
			if d.preferred != 4086 { // zero then non-zero -> non-zero adopted, no conflict
				t.Errorf("tcp/game preferred = %d, want 4086", d.preferred)
			}
		}
	}
	if len(conflicts) != 1 {
		t.Fatalf("conflicts = %v, want exactly one (tcp/ssh)", conflicts)
	}
}
