package controller

import (
	"errors"
	"testing"
)

func TestParseTruthy(t *testing.T) {
	for _, v := range []string{"true", "YES", " enable ", "Enabled"} {
		got, err := ParseTruthy(v)
		if err != nil || !got {
			t.Errorf("ParseTruthy(%q) = %v,%v; want true,nil", v, got, err)
		}
	}
	for _, v := range []string{"false", "no", "DISABLE", "disabled"} {
		got, err := ParseTruthy(v)
		if err != nil || got {
			t.Errorf("ParseTruthy(%q) = %v,%v; want false,nil", v, got, err)
		}
	}
	for _, v := range []string{"", "1", "0", "yep"} {
		if _, err := ParseTruthy(v); !errors.Is(err, ErrUnrecognizedTruthy) {
			t.Errorf("ParseTruthy(%q) err = %v; want ErrUnrecognizedTruthy", v, err)
		}
	}
}
