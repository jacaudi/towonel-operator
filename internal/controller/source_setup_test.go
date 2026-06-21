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

// TestGatewayAPISupported covers the gateway-api enablement decision that the
// envtest suite cannot isolate: that suite runs a single shared, gateway-ENABLED
// manager for all tests, so the disabled/degrade path (CRDs absent → sources off)
// has no per-test home there. Exercising the pure gatewayAPISupported directly
// pins all three branches: CRD present → (true,nil); NoMatch/CRDs absent →
// (false,nil) degrade; any other discovery error → (false,err) fail-fast.
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
