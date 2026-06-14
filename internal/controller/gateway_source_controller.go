package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GatewaySourceReconciler forwards a Gateway's listener hostnames to its proxy
// Service (design §4.1, forward-to-proxy).
type GatewaySourceReconciler struct {
	client.Client
	APIReader      client.Reader // uncached; used for authoritative GC-decision reads
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	AgentNamespace string
	sourceBase
}

//+kubebuilder:rbac:groups="gateway.networking.k8s.io",resources=gateways,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=towonel.io,resources=towonelagents,verbs=get;list;watch;create;update;patch;delete

func (r *GatewaySourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.ensure(r.Recorder)
	var gw gwv1.Gateway
	if err := r.Get(ctx, req.NamespacedName, &gw); err != nil {
		if apierrors.IsNotFound(err) {
			return releaseResult(r.releaseEverywhere(ctx, r.APIReader, r.Client, "Gateway", req.Namespace, req.Name))
		}
		return ctrl.Result{}, err
	}
	if enabled, _ := ParseTruthy(gw.Annotations[AnnotationTunnel]); !enabled {
		return releaseResult(r.releaseEverywhere(ctx, r.APIReader, r.Client, "Gateway", gw.Namespace, gw.Name))
	}
	emit := func(reason, msg string) { r.dedupe.emit(r.recorder, &gw, corev1.EventTypeWarning, reason, msg) }
	tunnel, err := parseTunnelRef(gw.Annotations[AnnotationTunnelRef], gw.Namespace)
	if err != nil {
		emit(ReasonTunnelRefMissing, err.Error())
		return ctrl.Result{}, nil
	}
	rt, ok := deriveGatewayRouting(ctx, r.Client, &gw, emit)
	if !ok {
		return ctrl.Result{}, nil
	}
	return r.applyContribution(ctx, r.Client, r.AgentNamespace, "Gateway", &gw, tunnel, gw.Annotations[AnnotationAgentRef], rt)
}

// parseGatewayServiceRef parses "[<ns>/]<name>[:<port>]" (port 0 => first port).
func parseGatewayServiceRef(raw, defNS string) (types.NamespacedName, int32, error) {
	raw = strings.TrimSpace(raw)
	var port int32
	if i := strings.LastIndexByte(raw, ':'); i >= 0 {
		p, err := strconv.Atoi(raw[i+1:])
		if err != nil || p <= 0 || p > 65535 {
			return types.NamespacedName{}, 0, fmt.Errorf("invalid port in gateway-service %q", raw)
		}
		port = int32(p)
		raw = raw[:i]
	}
	ns, name := defNS, raw
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		ns, name = raw[:i], raw[i+1:]
	}
	if ns == "" || name == "" {
		return types.NamespacedName{}, 0, fmt.Errorf("malformed gateway-service %q", raw)
	}
	return types.NamespacedName{Namespace: ns, Name: name}, port, nil
}

func deriveGatewayRouting(ctx context.Context, c client.Client, gw *gwv1.Gateway, emit func(string, string)) (routing, bool) {
	raw := gw.Annotations[AnnotationGatewayService]
	if raw == "" {
		emit(ReasonGatewayServiceUnset, "towonel.io/gateway-service is required on an opted-in Gateway (the proxy Service is not derivable from the Gateway object)")
		return routing{}, false
	}
	svcNN, port, err := parseGatewayServiceRef(raw, gw.Namespace)
	if err != nil {
		emit(ReasonInvalidAnnotation, err.Error())
		return routing{}, false
	}
	var svc corev1.Service
	if err := c.Get(ctx, svcNN, &svc); err != nil {
		emit(ReasonInvalidAnnotation, fmt.Sprintf("gateway-service %s not found: %v", svcNN, err))
		return routing{}, false
	}
	port, ok := effectiveServicePort(&svc, port)
	if !ok {
		emit(ReasonInvalidAnnotation, fmt.Sprintf("gateway-service %s has no ports; specify a port", svcNN))
		return routing{}, false
	}
	origin := originOf(svc.Spec.ClusterIP, port)
	var rt routing
	for _, l := range gw.Spec.Listeners {
		if l.Hostname == nil || *l.Hostname == "" {
			continue
		}
		rt.services = append(rt.services, map[string]any{"hostname": string(*l.Hostname), "origin": origin})
	}
	if rt.empty() {
		emit(ReasonInvalidAnnotation, "Gateway has no listener with a hostname to expose")
		return routing{}, false
	}
	return rt, true
}

func (r *GatewaySourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gwv1.Gateway{}, builder.WithPredicates(sourcePredicate())).
		Named("gateway-source").
		Complete(r)
}
