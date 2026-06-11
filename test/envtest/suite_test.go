package envtest_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/controller"
)

var (
	testEnv    *envtest.Environment
	sharedCfg  *rest.Config
	testScheme = runtime.NewScheme()
	k8sClient  client.Client // initialized in TestMain after the env starts

	// sharedSourceMgr is the single gateway-enabled source manager started by
	// TestMain and used by all source tests except TestGatewaySourcesDisabledWhenFlagFalse.
	// Sharing one manager avoids the watch-stream teardown/reopen race that
	// causes flakiness when each test starts its own manager.
	sharedSourceMgr ctrl.Manager
)

func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		fmt.Fprintln(os.Stderr, "skipping envtest suite: KUBEBUILDER_ASSETS unset")
		os.Exit(0)
	}
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(towonelv1alpha1.AddToScheme(testScheme))
	utilruntime.Must(gwv1.Install(testScheme))
	utilruntime.Must(gwv1beta1.Install(testScheme))

	// Resolve the gateway-api module dir at runtime so the CRD path stays
	// correct regardless of GOPATH layout.
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "sigs.k8s.io/gateway-api").Output()
	if err != nil {
		log.Fatalf("locate gateway-api module: %v", err)
	}
	gwCRDs := filepath.Join(strings.TrimSpace(string(out)), "config", "crd", "standard")

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			gwCRDs,
		},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest start: %v\n", err)
		os.Exit(1)
	}
	sharedCfg = cfg
	k8sClient, err = client.New(sharedCfg, client.Options{Scheme: testScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "k8sClient init: %v\n", err)
		os.Exit(1)
	}

	// Start a single shared source manager (gateway-api enabled) for all source
	// tests. This avoids per-test manager startup/teardown races that cause flakes
	// when the Service informer watch stream is torn down and reopened rapidly.
	sharedSourceMgr, err = ctrl.NewManager(sharedCfg, ctrl.Options{
		Scheme:  testScheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		Controller: crconfig.Controller{
			SkipNameValidation: ptr.To(true),
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "source manager init: %v\n", err)
		os.Exit(1)
	}
	if err := controller.RegisterIndexes(context.Background(), sharedSourceMgr); err != nil {
		fmt.Fprintf(os.Stderr, "source manager RegisterIndexes: %v\n", err)
		os.Exit(1)
	}
	if err := controller.SetupSourceControllers(sharedSourceMgr, controller.SourceConfig{
		AgentNamespace:   "",
		EnableGatewayAPI: "true",
	}); err != nil {
		fmt.Fprintf(os.Stderr, "source manager SetupSourceControllers: %v\n", err)
		os.Exit(1)
	}
	sharedSourceCtx, sharedSourceCancel := context.WithCancel(context.Background())
	go func() { _ = sharedSourceMgr.Start(sharedSourceCtx) }()
	if !sharedSourceMgr.GetCache().WaitForCacheSync(sharedSourceCtx) {
		fmt.Fprintln(os.Stderr, "source manager cache sync failed")
		sharedSourceCancel()
		os.Exit(1)
	}
	// Wait for the manager to be "elected" (leader-election runnables phase),
	// which means the controller event sources (priority queues) have started.
	// WaitForCacheSync only gates on the cache informers; the controllers
	// themselves are started in a separate goroutine AFTER the cache warmup.
	// Without this wait, tests that create objects immediately after
	// WaitForCacheSync may race against the controller startup.
	select {
	case <-sharedSourceMgr.Elected():
	case <-sharedSourceCtx.Done():
		fmt.Fprintln(os.Stderr, "source manager never reached elected state")
		os.Exit(1)
	}

	code := m.Run()
	sharedSourceCancel()
	_ = testEnv.Stop()
	os.Exit(code)
}

// mustNamespace creates a fresh namespace with a generated name and returns it.
// The namespace is not cleaned up — envtest tears down the whole env after the
// suite, so per-test isolation is achieved by using a distinct namespace per test.
func mustNamespace(t *testing.T) string {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "src-test-"}}
	if err := k8sClient.Create(context.Background(), ns); err != nil {
		t.Fatal(err)
	}
	return ns.Name
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

// startSourceManagerWith starts a manager running the source controllers with
// the given gateway-api enablement string ("true"/"false"/"auto"), stopping at
// test end via t.Cleanup. Blocks until the cache is synced so tests can
// immediately create resources and rely on reconciles being triggered.
// This is only needed for tests that require a non-default manager configuration
// (e.g. EnableGatewayAPI:"false"). Most source tests use sharedSourceMgr instead.
// GracefulShutdownTimeout is set to 1s so the per-test manager's Service informer
// watch connection is closed quickly, preventing interference with the shared manager.
func startSourceManagerWith(t *testing.T, agentNS, enable string) {
	t.Helper()
	shortShutdown := 1 * time.Second
	opts := managerOptions()
	opts.GracefulShutdownTimeout = &shortShutdown
	mgr, err := ctrl.NewManager(sharedCfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.RegisterIndexes(context.Background(), mgr); err != nil {
		t.Fatal(err)
	}
	if err := controller.SetupSourceControllers(mgr, controller.SourceConfig{
		AgentNamespace:   agentNS,
		EnableGatewayAPI: enable,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = mgr.Start(ctx)
	}()
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		cancel()
		<-stopped
		t.Fatal("source manager cache sync failed")
	}
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
}
