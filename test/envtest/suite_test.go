package envtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	crconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

var (
	testEnv    *envtest.Environment
	sharedCfg  *rest.Config
	testScheme = runtime.NewScheme()
)

func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		fmt.Fprintln(os.Stderr, "skipping envtest suite: KUBEBUILDER_ASSETS unset")
		os.Exit(0)
	}
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(towonelv1alpha1.AddToScheme(testScheme))

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest start: %v\n", err)
		os.Exit(1)
	}
	sharedCfg = cfg
	code := m.Run()
	_ = testEnv.Stop()
	os.Exit(code)
}

// managerOptions returns ctrl.Options suitable for per-test managers.
// Metrics are disabled (BindAddress "0") to avoid port collisions.
// SkipNameValidation suppresses duplicate-controller-name errors when multiple
// managers sharing the same Named("towoneltunnel") controller are started in
// parallel tests.
func managerOptions() ctrl.Options {
	return ctrl.Options{
		Scheme:  testScheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		Controller: crconfig.Controller{
			SkipNameValidation: ptr.To(true),
		},
	}
}
