package controller

import "testing"

func TestPortLabel(t *testing.T) {
	if got := portLabel("net", "app", "ssh"); got != "net/app/ssh" {
		t.Errorf("portLabel = %q", got)
	}
	if got := portLabelPrefix("net", "app"); got != "net/app/" {
		t.Errorf("portLabelPrefix = %q", got)
	}
}
