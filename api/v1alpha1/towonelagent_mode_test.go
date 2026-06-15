package v1alpha1

import "testing"

func TestAgentManagementModeConstants(t *testing.T) {
	if ModeManaged != "Managed" {
		t.Errorf("ModeManaged = %q, want Managed", ModeManaged)
	}
	if ModeObserveOnly != "ObserveOnly" {
		t.Errorf("ModeObserveOnly = %q, want ObserveOnly", ModeObserveOnly)
	}
	// The field must be settable with the typed constants.
	var s TowonelAgentSpec
	s.Mode = ModeManaged
	if s.Mode != ModeManaged {
		t.Fatal("Spec.Mode not settable to ModeManaged")
	}
}
