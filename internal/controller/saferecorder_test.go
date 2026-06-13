package controller

import "testing"

func TestSafeRecorderNilSafe(t *testing.T) {
	var sr *SafeRecorder                                     // typed nil
	sr.Event(nil, "Normal", "R", "m")                        // must not panic
	NewSafeRecorder(nil).Eventf(nil, "Normal", "R", "%d", 1) // wraps nil recorder
}
