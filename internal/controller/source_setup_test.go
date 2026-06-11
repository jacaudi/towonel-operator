package controller

import (
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type stubMapper struct {
	meta.RESTMapper
	err error
}

func (s stubMapper) RESTMapping(_ schema.GroupKind, _ ...string) (*meta.RESTMapping, error) {
	return nil, s.err
}

func TestGatewayAPISupported(t *testing.T) {
	if ok, err := gatewayAPISupported(stubMapper{err: nil}); !ok || err != nil {
		t.Fatalf("present: ok=%v err=%v", ok, err)
	}
	if ok, err := gatewayAPISupported(stubMapper{err: &meta.NoKindMatchError{}}); ok || err != nil {
		t.Fatalf("absent: ok=%v err=%v", ok, err)
	}
	if ok, err := gatewayAPISupported(stubMapper{err: errors.New("boom")}); ok || err == nil {
		t.Fatalf("discovery error: ok=%v err=%v", ok, err)
	}
}
