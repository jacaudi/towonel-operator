package controller

import (
	"context"
	"fmt"

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
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// HTTPRouteSourceReconciler forwards a route's hostnames through its parent
// Gateway's proxy: it walks parentRefs to each parent Gateway, resolves that
// Gateway's towonel.io/gateway-service annotation to a ClusterIP:port origin,
// and exposes the route's hostnames against that single proxy (design §4.1).
type HTTPRouteSourceReconciler struct {
	client.Client
	APIReader      client.Reader // uncached; used for authoritative GC-decision reads
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	AgentNamespace string
	sourceBase
}

//+kubebuilder:rbac:groups="gateway.networking.k8s.io",resources=httproutes,verbs=get;list;watch
//+kubebuilder:rbac:groups="gateway.networking.k8s.io",resources=gateways,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=towonel.io,resources=towonelagents,verbs=get;list;watch;create;update;patch;delete

func (r *HTTPRouteSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.ensure(r.Recorder)
	var rtObj gwv1.HTTPRoute
	if err := r.Get(ctx, req.NamespacedName, &rtObj); err != nil {
		if apierrors.IsNotFound(err) {
			return releaseResult(r.releaseEverywhere(ctx, r.APIReader, r.Client, "HTTPRoute", req.Namespace, req.Name))
		}
		return ctrl.Result{}, err
	}
	if enabled, _ := ParseTruthy(rtObj.Annotations[AnnotationTunnel]); !enabled {
		return releaseResult(r.releaseEverywhere(ctx, r.APIReader, r.Client, "HTTPRoute", rtObj.Namespace, rtObj.Name))
	}
	emit := func(reason, msg string) { r.dedupe.emit(r.recorder, &rtObj, corev1.EventTypeWarning, reason, msg) }
	tunnel, err := parseTunnelRef(rtObj.Annotations[AnnotationTunnelRef], rtObj.Namespace)
	if err != nil {
		emit(ReasonTunnelRefMissing, err.Error())
		return ctrl.Result{}, nil
	}
	rt, ok, derr := r.deriveHTTPRouteRouting(ctx, &rtObj, emit)
	if derr != nil {
		return ctrl.Result{}, derr // transient (e.g. a Get failed) — requeue
	}
	if !ok {
		return ctrl.Result{}, nil
	}
	return r.applyContribution(ctx, r.Client, r.AgentNamespace, "HTTPRoute", &rtObj, tunnel, rtObj.Annotations[AnnotationAgentRef], rt)
}

// isGatewayParent reports whether a parentRef targets a Gateway. Group/Kind are
// pointers that default server-side when omitted, so nil == the Gateway default.
func isGatewayParent(p gwv1.ParentReference) bool {
	group := "gateway.networking.k8s.io"
	if p.Group != nil {
		group = string(*p.Group)
	}
	kind := "Gateway"
	if p.Kind != nil {
		kind = string(*p.Kind)
	}
	return group == "gateway.networking.k8s.io" && kind == "Gateway"
}

// resolveParentGatewayProxy walks parentRefs, resolves each parent Gateway's
// towonel.io/gateway-service proxy to a ClusterIP:port origin, and returns the
// single distinct origin. (origin, true, nil) on success; ("", false, nil) with
// an emitted Event on a skip case; ("", false, err) on a transient API error.
func (r *HTTPRouteSourceReconciler) resolveParentGatewayProxy(ctx context.Context, rtObj *gwv1.HTTPRoute, emit func(string, string)) (string, bool, error) {
	origins := map[string]struct{}{}
	for _, p := range rtObj.Spec.ParentRefs {
		if !isGatewayParent(p) {
			continue
		}
		ns := rtObj.Namespace
		if p.Namespace != nil {
			ns = string(*p.Namespace)
		}
		var gw gwv1.Gateway
		if err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: string(p.Name)}, &gw); err != nil {
			if apierrors.IsNotFound(err) {
				emit(ReasonInvalidAnnotation, fmt.Sprintf("parent Gateway %s/%s not found", ns, p.Name))
				continue
			}
			return "", false, err // transient — requeue
		}
		raw := gw.Annotations[AnnotationGatewayService]
		if raw == "" {
			emit(ReasonGatewayServiceUnset, fmt.Sprintf("parent Gateway %s/%s has no %s annotation", ns, gw.Name, AnnotationGatewayService))
			continue
		}
		svcNN, port, err := parseGatewayServiceRef(raw, gw.Namespace)
		if err != nil {
			emit(ReasonInvalidAnnotation, err.Error())
			continue
		}
		var svc corev1.Service
		if err := r.Get(ctx, svcNN, &svc); err != nil {
			if apierrors.IsNotFound(err) {
				emit(ReasonInvalidAnnotation, fmt.Sprintf("gateway-service %s not found", svcNN))
				continue
			}
			return "", false, err // transient — requeue
		}
		eport, ok := effectiveServicePort(&svc, port)
		if !ok {
			emit(ReasonInvalidAnnotation, fmt.Sprintf("gateway-service %s has no ports; specify a port", svcNN))
			continue
		}
		origins[originOf(svc.Spec.ClusterIP, eport)] = struct{}{}
	}
	switch len(origins) {
	case 0:
		emit(ReasonGatewayServiceUnset, "no parent Gateway with a usable towonel.io/gateway-service")
		return "", false, nil
	case 1:
		for o := range origins {
			return o, true, nil
		}
	}
	emit(ReasonAmbiguousGateway, fmt.Sprintf("parentRefs resolve to %d distinct gateway proxies; one origin is required — name them on separate tunnels or use an explicit TowonelAgent", len(origins)))
	return "", false, nil
}

// deriveHTTPRouteRouting emits one passthrough HTTPS entry per spec.hostnames,
// origin = the parent Gateway's proxy. (routing, ok, error); error is transient.
func (r *HTTPRouteSourceReconciler) deriveHTTPRouteRouting(ctx context.Context, rtObj *gwv1.HTTPRoute, emit func(string, string)) (routing, bool, error) {
	if len(rtObj.Spec.Hostnames) == 0 {
		emit(ReasonInvalidAnnotation, "HTTPRoute has no spec.hostnames to expose")
		return routing{}, false, nil
	}
	origin, ok, err := r.resolveParentGatewayProxy(ctx, rtObj, emit)
	if err != nil {
		return routing{}, false, err
	}
	if !ok {
		return routing{}, false, nil
	}
	var out routing
	for _, h := range rtObj.Spec.Hostnames {
		// No edgeTLSMode set → CRD default `passthrough`: the Towonel edge peeks SNI
		// and forwards raw TLS; the origin (behind the parent gateway proxy) terminates.
		// Same routing shape as the Gateway source. DRY: mirrors deriveGatewayRouting.
		out.services = append(out.services, map[string]any{"hostname": string(h), "origin": origin})
	}
	return out, true, nil
}

// routesForGateway enqueues every tunnel-annotated HTTPRoute whose parentRef
// targets the changed Gateway, so a change to that Gateway's gateway-service
// annotation (or proxy Service) re-flows to dependent routes. The presence of
// AnnotationTunnel is a cheap first-cut filter; Reconcile re-checks truthiness.
func (r *HTTPRouteSourceReconciler) routesForGateway(ctx context.Context, obj client.Object) []reconcile.Request {
	gw, ok := obj.(*gwv1.Gateway)
	if !ok {
		return nil
	}
	var routes gwv1.HTTPRouteList
	if err := r.List(ctx, &routes); err != nil {
		logf.FromContext(ctx).Error(err, "routesForGateway: list failed", "gateway", client.ObjectKeyFromObject(gw))
		return nil
	}
	var reqs []reconcile.Request
	for i := range routes.Items {
		rt := &routes.Items[i]
		if _, opted := rt.Annotations[AnnotationTunnel]; !opted {
			continue
		}
		for _, p := range rt.Spec.ParentRefs {
			if !isGatewayParent(p) {
				continue
			}
			ns := rt.Namespace // parentRef nil namespace defaults to the ROUTE's namespace
			if p.Namespace != nil {
				ns = string(*p.Namespace)
			}
			if ns == gw.Namespace && string(p.Name) == gw.Name {
				reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: rt.Namespace, Name: rt.Name}})
				break
			}
		}
	}
	return reqs
}

// sourcesForAgent enqueues every tunnel-annotated HTTPRoute whose towonel.io/agent-ref
// resolves to the changed TowonelAgent, so a route that opted in before its agent
// existed re-flows once the agent appears (#22). Matching is delegated to the shared
// sourceTargetsAgent predicate (mirrors resolveTarget). The AnnotationTunnel presence
// check is a cheap first-cut filter; Reconcile re-checks truthiness.
func (r *HTTPRouteSourceReconciler) sourcesForAgent(ctx context.Context, obj client.Object) []reconcile.Request {
	ta, ok := obj.(*towonelv1alpha1.TowonelAgent)
	if !ok {
		return nil
	}
	var routes gwv1.HTTPRouteList
	if err := r.List(ctx, &routes); err != nil {
		logf.FromContext(ctx).Error(err, "sourcesForAgent: list failed", "agent", client.ObjectKeyFromObject(ta))
		return nil
	}
	var reqs []reconcile.Request
	for i := range routes.Items {
		rt := &routes.Items[i]
		if _, opted := rt.Annotations[AnnotationTunnel]; !opted {
			continue
		}
		if sourceTargetsAgent(rt.Annotations, rt.Namespace, r.AgentNamespace, ta) {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: rt.Namespace, Name: rt.Name}})
		}
	}
	return reqs
}

func (r *HTTPRouteSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gwv1.HTTPRoute{}, builder.WithPredicates(sourcePredicate())).
		Watches(&gwv1.Gateway{}, handler.EnqueueRequestsFromMapFunc(r.routesForGateway), builder.WithPredicates(crossWatchPredicate())).
		Watches(&towonelv1alpha1.TowonelAgent{}, handler.EnqueueRequestsFromMapFunc(r.sourcesForAgent), builder.WithPredicates(crossWatchPredicate())).
		Named("httproute-source").
		Complete(r)
}
