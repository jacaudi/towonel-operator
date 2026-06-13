// Command manager runs the towonel-operator controllers.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
	"github.com/jacaudi/towonel-operator/internal/controller"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// Build metadata, injected via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = ""
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(towonelv1alpha1.AddToScheme(scheme))
	utilruntime.Must(gwv1.Install(scheme))
	utilruntime.Must(gwv1beta1.Install(scheme))
}

func main() {
	var (
		metricsAddr   string
		probeAddr     string
		leaderElect   bool
		showVersion   bool
		towonelAPIURL string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "metrics endpoint bind address")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "health probe bind address")
	flag.BoolVar(&leaderElect, "leader-elect", true, "enable leader election for HA")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.StringVar(&towonelAPIURL, "towonel-api-url", "https://console.towonel.dev", "Towonel hub base URL")
	var agentNamespace, enableGatewayAPI string
	flag.StringVar(&agentNamespace, "agent-namespace", "", "namespace for auto-created default agents (empty = the tunnel's namespace)")
	flag.StringVar(&enableGatewayAPI, "enable-gateway-api", "auto", "auto|true|false — register Gateway/HTTPRoute source controllers")
	zapOpts := zap.Options{Development: false}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	if showVersion {
		fmt.Printf("towonel-operator %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr, TLSOpts: []func(*tls.Config){}},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "towonel-operator.towonel.io",
		// Pin leader election to the operator's own namespace from the downward
		// API (POD_NAMESPACE, injected by the chart). Empty falls back to
		// controller-runtime's in-cluster namespace auto-detection.
		LeaderElectionNamespace: os.Getenv("POD_NAMESPACE"),
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := controller.RegisterIndexes(context.Background(), mgr); err != nil {
		setupLog.Error(err, "unable to register field indexes")
		os.Exit(1)
	}

	if err := (&controller.TowonelTunnelReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Recorder:   mgr.GetEventRecorderFor("towoneltunnel"),
		BaseURL:    towonelAPIURL,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TowonelTunnel")
		os.Exit(1)
	}
	if err := (&controller.TowonelAgentReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("towonelagent"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TowonelAgent")
		os.Exit(1)
	}

	if err := controller.SetupSourceControllers(mgr, controller.SourceConfig{
		AgentNamespace:   agentNamespace,
		EnableGatewayAPI: enableGatewayAPI,
	}); err != nil {
		setupLog.Error(err, "unable to set up source controllers")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager", "version", version)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
