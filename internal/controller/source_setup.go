package controller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
)

// SourceConfig configures the source-layer controllers (12-Factor: from flags).
type SourceConfig struct {
	AgentNamespace   string // --agent-namespace ("" => tunnel's namespace)
	EnableGatewayAPI string // "auto" | "true" | "false"
}

// gatewayAPISupported reports whether the cluster serves the gateway-api kinds.
// (false,nil)=CRDs absent (degrade); (false,err)=discovery failed (fail fast).
func gatewayAPISupported(rm meta.RESTMapper) (bool, error) {
	_, err := rm.RESTMapping(schema.GroupKind{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute"}, "v1")
	if err == nil {
		return true, nil
	}
	if meta.IsNoMatchError(err) {
		return false, nil
	}
	return false, err
}

// SetupSourceControllers registers the Service source unconditionally and the
// Gateway/HTTPRoute sources only when the gateway-api CRDs are present (design §8).
// The SCHEME is installed unconditionally in main (harmless when CRDs are absent);
// only the WATCH is gated here.
func SetupSourceControllers(mgr ctrl.Manager, cfg SourceConfig) error {
	if err := (&ServiceSourceReconciler{
		Client:         mgr.GetClient(),
		APIReader:      mgr.GetAPIReader(),
		Scheme:         mgr.GetScheme(),
		Recorder:       mgr.GetEventRecorderFor("service-source"),
		AgentNamespace: cfg.AgentNamespace,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setup ServiceSource: %w", err)
	}

	enable := false
	switch cfg.EnableGatewayAPI {
	case "true":
		enable = true
	case "false":
		enable = false
	default: // "auto"
		ok, err := gatewayAPISupported(mgr.GetRESTMapper())
		if err != nil {
			return fmt.Errorf("probe gateway-api support: %w", err)
		}
		enable = ok
		if !ok {
			ctrl.Log.WithName("source").Info("gateway-api CRDs absent — Gateway/HTTPRoute sources disabled; Service shim unaffected")
		}
	}
	if !enable {
		return nil
	}
	if err := (&GatewaySourceReconciler{
		Client:         mgr.GetClient(),
		APIReader:      mgr.GetAPIReader(),
		Scheme:         mgr.GetScheme(),
		Recorder:       mgr.GetEventRecorderFor("gateway-source"),
		AgentNamespace: cfg.AgentNamespace,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setup GatewaySource: %w", err)
	}
	if err := (&HTTPRouteSourceReconciler{
		Client:         mgr.GetClient(),
		APIReader:      mgr.GetAPIReader(),
		Scheme:         mgr.GetScheme(),
		Recorder:       mgr.GetEventRecorderFor("httproute-source"),
		AgentNamespace: cfg.AgentNamespace,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setup HTTPRouteSource: %w", err)
	}
	return nil
}
