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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// ServiceSourceReconciler emits routing from annotated Services (design §4.1).
type ServiceSourceReconciler struct {
	client.Client
	APIReader      client.Reader // uncached; used for authoritative GC-decision reads
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	AgentNamespace string // "" => tunnel's namespace
	sourceBase
}

//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=towonel.io,resources=towonelagents,verbs=get;list;watch;create;update;patch;delete

func (r *ServiceSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.ensure(r.Recorder)
	var svc corev1.Service
	if err := r.Get(ctx, req.NamespacedName, &svc); err != nil {
		if apierrors.IsNotFound(err) {
			return releaseResult(r.releaseEverywhere(ctx, r.APIReader, r.Client, "Service", req.Namespace, req.Name))
		}
		return ctrl.Result{}, err
	}
	if enabled, _ := ParseTruthy(svc.Annotations[AnnotationTunnel]); !enabled {
		return releaseResult(r.releaseEverywhere(ctx, r.APIReader, r.Client, "Service", svc.Namespace, svc.Name))
	}
	emit := func(reason, msg string) { r.dedupe.emit(r.recorder, &svc, corev1.EventTypeWarning, reason, msg) }
	tunnel, err := parseTunnelRef(svc.Annotations[AnnotationTunnelRef], svc.Namespace)
	if err != nil {
		emit(ReasonTunnelRefMissing, err.Error())
		return ctrl.Result{}, nil
	}
	rt, ok := r.deriveServiceRouting(&svc, emit)
	if !ok {
		return ctrl.Result{}, nil // derive emitted an Event; never prune on this path
	}
	if rt.empty() {
		return releaseResult(r.releaseEverywhere(ctx, r.APIReader, r.Client, "Service", svc.Namespace, svc.Name))
	}
	return r.applyContribution(ctx, r.Client, r.AgentNamespace, "Service", &svc, tunnel, svc.Annotations[AnnotationAgentRef], rt)
}

// originOf formats a ClusterIP:port string for a routing entry.
func originOf(host string, port int32) string { return fmt.Sprintf("%s:%d", host, port) }

// effectiveServicePort resolves a gateway-service port: a non-zero port is used
// as-is; port 0 means "first Service port" (the parseGatewayServiceRef contract).
// ok is false when port is 0 and the Service exposes no ports.
func effectiveServicePort(svc *corev1.Service, port int32) (int32, bool) {
	if port != 0 {
		return port, true
	}
	if len(svc.Spec.Ports) == 0 {
		return 0, false
	}
	return svc.Spec.Ports[0].Port, true
}

// parsePortScopedKey splits "towonel.io/<port>.<suffix>" on the LAST dot (port
// names are DNS-1123 → no dots).
func parsePortScopedKey(key string) (port, suffix string, ok bool) {
	const p = "towonel.io/"
	if !strings.HasPrefix(key, p) {
		return "", "", false
	}
	rest := key[len(p):]
	i := strings.LastIndexByte(rest, '.')
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	switch s := rest[i+1:]; s {
	case "hostname", "tcp", "udp", "public-port":
		return rest[:i], s, true
	}
	return "", "", false
}

func (r *ServiceSourceReconciler) deriveServiceRouting(svc *corev1.Service, emit func(string, string)) (routing, bool) {
	clusterIP := svc.Spec.ClusterIP
	portByName := map[string]int32{}
	for _, p := range svc.Spec.Ports {
		portByName[p.Name] = p.Port
	}

	// Port-scoped keys (multi-exposure) take precedence.
	var rt routing
	scoped := false
	for k, v := range svc.Annotations {
		name, suffix, ok := parsePortScopedKey(k)
		if !ok {
			continue
		}
		scoped = true
		if suffix == "public-port" {
			continue // applied after the loop
		}
		port, found := portByName[name]
		if !found {
			emit(ReasonInvalidAnnotation, fmt.Sprintf("annotation %q names port %q not declared on the Service", k, name))
			return routing{}, false
		}
		origin := originOf(clusterIP, port)
		switch suffix {
		case "hostname":
			rt.services = append(rt.services, map[string]any{"hostname": v, "origin": origin})
		case "tcp":
			rt.tcp = append(rt.tcp, map[string]any{"name": name, "origin": origin})
		case "udp":
			rt.udp = append(rt.udp, map[string]any{"name": name, "origin": origin})
		}
	}
	if scoped {
		r.applyPublicPorts(svc.Annotations, &rt, emit)
		if rt.empty() {
			emit(ReasonInvalidAnnotation, "port-scoped annotations present but resolved to no exposure")
			return routing{}, false
		}
		return rt, true
	}

	// Single-exposure.
	origin := svc.Annotations[AnnotationSrcOrigin]
	if origin == "" {
		if len(svc.Spec.Ports) == 0 || clusterIP == "" || clusterIP == corev1.ClusterIPNone {
			emit(ReasonInvalidAnnotation, "no towonel.io/origin and the Service has no ClusterIP:port to default to")
			return routing{}, false
		}
		origin = originOf(clusterIP, svc.Spec.Ports[0].Port)
	}
	switch strings.ToLower(strings.TrimSpace(svc.Annotations[AnnotationSrcProtocol])) {
	case "tcp":
		rt.tcp = []map[string]any{{"name": svc.Name, "origin": origin}}
	case "udp":
		rt.udp = []map[string]any{{"name": svc.Name, "origin": origin}}
	case "":
		host := svc.Annotations[AnnotationSrcHostname]
		if host == "" {
			emit(ReasonInvalidAnnotation, "opted in but no towonel.io/hostname (HTTPS) or towonel.io/protocol (raw) declared")
			return routing{}, false
		}
		entry := map[string]any{"hostname": host, "origin": origin}
		if m := svc.Annotations[AnnotationSrcEdgeTLSMode]; m != "" {
			entry["edgeTLSMode"] = m // CRD JSON tag is camelCase `edgeTLSMode` — a wrong key is pruned silently
		}
		rt.services = []map[string]any{entry}
	default:
		emit(ReasonInvalidAnnotation, fmt.Sprintf("towonel.io/protocol %q must be tcp or udp", svc.Annotations[AnnotationSrcProtocol]))
		return routing{}, false
	}
	return rt, true
}

// applyPublicPorts attaches int64 preferredPort to the tcp/udp entry whose name
// matches a "<name>.public-port" annotation key.
func (r *ServiceSourceReconciler) applyPublicPorts(ann map[string]string, rt *routing, emit func(string, string)) {
	for k, v := range ann {
		name, suffix, ok := parsePortScopedKey(k)
		if !ok || suffix != "public-port" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 65535 {
			emit(ReasonInvalidAnnotation, fmt.Sprintf("%q must be a valid port number", k))
			continue
		}
		for _, list := range [][]map[string]any{rt.tcp, rt.udp} {
			for _, e := range list {
				if e["name"] == name {
					e["preferredPort"] = int64(n) // must be int64: unstructured deep-copy rejects int32
				}
			}
		}
	}
}

// sourcesForAgent enqueues every tunnel-annotated Service whose towonel.io/agent-ref
// resolves to the changed TowonelAgent, so a Service that opted in before its agent
// existed re-flows once the agent appears (#22). See sourceTargetsAgent for matching
// (explicit agent-ref only; the default-agent path cannot strand).
func (r *ServiceSourceReconciler) sourcesForAgent(ctx context.Context, obj client.Object) []reconcile.Request {
	ta, ok := obj.(*towonelv1alpha1.TowonelAgent)
	if !ok {
		return nil
	}
	var svcs corev1.ServiceList
	if err := r.List(ctx, &svcs); err != nil {
		logf.FromContext(ctx).Error(err, "sourcesForAgent: list failed", "agent", client.ObjectKeyFromObject(ta))
		return nil
	}
	var reqs []reconcile.Request
	for i := range svcs.Items {
		svc := &svcs.Items[i]
		if _, opted := svc.Annotations[AnnotationTunnel]; !opted {
			continue
		}
		if sourceTargetsAgent(svc.Annotations, svc.Namespace, r.AgentNamespace, ta) {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}})
		}
	}
	return reqs
}

func (r *ServiceSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Service{}, builder.WithPredicates(sourcePredicate())).
		Watches(&towonelv1alpha1.TowonelAgent{}, handler.EnqueueRequestsFromMapFunc(r.sourcesForAgent), builder.WithPredicates(crossWatchPredicate())).
		Named("service-source").
		Complete(r)
}
