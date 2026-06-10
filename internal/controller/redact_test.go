package controller

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestSecretRedacts(t *testing.T) {
	s := secret("twk_supersecret")
	for _, got := range []string{fmt.Sprintf("%s", s), fmt.Sprintf("%v", s), fmt.Sprintf("%#v", s)} {
		if got != "[REDACTED]" {
			t.Errorf("format leaked secret: %q", got)
		}
	}
	b, err := json.Marshal(struct{ Key secret }{s})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `{"Key":"[REDACTED]"}` {
		t.Errorf("json leaked secret: %s", b)
	}
	txt, err := s.MarshalText()
	if err != nil {
		t.Fatalf("marshal text: %v", err)
	}
	if string(txt) != "[REDACTED]" {
		t.Errorf("text leaked secret: %s", txt)
	}
	if s.Expose() != "twk_supersecret" {
		t.Errorf("Expose() = %q", s.Expose())
	}
}
