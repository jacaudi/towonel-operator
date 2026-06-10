package v1alpha1

import (
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

func TestAgentStatusHasObservedGeneration(t *testing.T) {
	st := TowonelAgentStatus{ObservedGeneration: 7}
	if st.ObservedGeneration != 7 {
		t.Fatal("ObservedGeneration not settable")
	}
}
