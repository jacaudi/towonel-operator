package v1alpha1

import (
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestSchemeRegistersKinds(t *testing.T) {
	s := runtime.NewScheme()
	if err := AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	for _, kind := range []string{"TowonelTunnel", "TowonelTunnelList", "TowonelAgent", "TowonelAgentList"} {
		gvk := schema.GroupVersionKind{Group: GroupVersion.Group, Version: GroupVersion.Version, Kind: kind}
		if !s.Recognizes(gvk) {
			t.Errorf("scheme does not recognize %s", gvk)
		}
	}
}

func TestAgentStatusObservedGenerationJSON(t *testing.T) {
	b, err := json.Marshal(TowonelAgentStatus{ObservedGeneration: 7})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"observedGeneration":7`) {
		t.Fatalf("json = %s", b)
	}
	empty, err := json.Marshal(TowonelAgentStatus{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "observedGeneration") {
		t.Fatalf("omitempty violated: %s", empty)
	}
}
