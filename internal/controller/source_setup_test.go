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

// TestGatewayEnable pins the flag DISPATCH that the envtest suite cannot reach
// (it runs a single shared, gateway-ENABLED manager): explicit "true"/"false"
// are honored verbatim, while "auto" (and "") probe the cluster — CRDs present
// → enabled, absent → disabled/degrade, discovery error → fail fast. The
// "false" → disabled arm is the unit-level proof that auto-routes is inert when
// gateway-api is disabled.
func TestGatewayEnable(t *testing.T) {
	cases := []struct {
		name    string
		flag    string
		mapErr  error // RESTMapping error for the "auto" probe (ignored for true/false)
		want    bool
		wantErr bool
	}{
		{"explicit true", "true", nil, true, false},
		{"explicit false → disabled (auto-routes inert)", "false", nil, false, false},
		{"auto + CRDs present", "auto", nil, true, false},
		{"auto + CRDs absent → degrade", "auto", &meta.NoKindMatchError{}, false, false},
		{"auto + discovery error → fail fast", "auto", errors.New("boom"), false, true},
		{"empty defaults to auto", "", nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gatewayEnable(SourceConfig{EnableGatewayAPI: tc.flag}, stubMapper{err: tc.mapErr})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got=%v want=%v", got, tc.want)
			}
		})
	}
}
