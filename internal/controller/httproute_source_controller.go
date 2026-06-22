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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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

// httpRouteSourcePredicate admits an HTTPRoute to the reconcile queue when it
// carries the towonel.io/tunnel annotation OR has a Gateway parentRef. The
// second clause lets an UN-annotated route reach Reconcile so it can inherit a
// parent Gateway's towonel.io/auto-routes default (#25) — the authoritative
// decision needs a client (to read the Gateway) and is made in Reconcile, not
// here. This is the HTTPRoute-specific broadening of the shared, annotation-only
// sourcePredicate (source_base.go); Gateway/Service keep sourcePredicate.
func httpRouteSourcePredicate() predicate.Predicate {
	admit := func(obj client.Object) bool {
		if _, ok := obj.GetAnnotations()[AnnotationTunnel]; ok {
			return true
		}
		rt, ok := obj.(*gwv1.HTTPRoute)
		if !ok {
			return false
		}
		for _, p := range rt.Spec.ParentRefs {
			if isGatewayParent(p) {
				return true
			}
		}
		return false
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return admit(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return admit(e.ObjectOld) || admit(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return admit(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return admit(e.Object) },
	}
}

// httpRouteForPredicate gates the broadened admit predicate with a generation/
// annotation churn filter so status-only writes from gateway controllers (which
// bump neither metadata.generation nor annotations) do not drive full no-op
// reconciles. Create/Delete are unaffected (the embedded predicates override only
// Update); a route's own selection can change via its spec (generation) or its
// annotations, and the parent-Gateway path is covered by the routesForGateway
// Watch. Applied to HTTPRoute only — Gateway/Service keep the annotation-only
// sourcePredicate, so their churn is already bounded to opted-in objects.
func httpRouteForPredicate() predicate.Predicate {
	return predicate.And(
		httpRouteSourcePredicate(),
		predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}),
	)
}

// autoSelectedByGateway reports whether an un-annotated HTTPRoute is auto-selected
// for tunneling by a parent Gateway that opted in via towonel.io/auto-routes (#25).
// Namespace-scoped: only a parent Gateway in the route's OWN namespace counts
// (design §2). The Gateway must also carry towonel.io/gateway-service (the proxy
// origin routes flow THROUGH) — an auto-routes Gateway without it is a no-op and
// emits ReasonGatewayServiceUnset. Returns (true, nil) on the first eligible
// parent; (false, err) only on a transient Get failure (caller requeues).
func (r *HTTPRouteSourceReconciler) autoSelectedByGateway(ctx context.Context, rtObj *gwv1.HTTPRoute, emit func(string, string)) (bool, error) {
	for _, p := range rtObj.Spec.ParentRefs {
		if !isGatewayParent(p) {
			continue
		}
		// Namespace scoping: a nil parentRef namespace defaults to the route's own
		// namespace; an explicit cross-namespace parentRef is never auto-selected.
		if parentRefNamespace(p, rtObj.Namespace) != rtObj.Namespace {
			continue
		}
		var gw gwv1.Gateway
		if err := r.Get(ctx, types.NamespacedName{Namespace: rtObj.Namespace, Name: string(p.Name)}, &gw); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, err // transient — requeue
		}
		if enabled, _ := ParseTruthy(gw.Annotations[AnnotationAutoRoutes]); !enabled {
			continue
		}
		// Hard prerequisite: routes route through the parent gateway proxy.
		if gw.Annotations[AnnotationGatewayService] == "" {
			emit(ReasonGatewayServiceUnset, fmt.Sprintf("Gateway %s/%s has %s but no %s; cannot auto-tunnel its routes (no origin to forward to)", gw.Namespace, gw.Name, AnnotationAutoRoutes, AnnotationGatewayService))
			continue
		}
		return true, nil
	}
	return false, nil
}

func (r *HTTPRouteSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.ensure(r.Recorder)
	var rtObj gwv1.HTTPRoute
	if err := r.Get(ctx, req.NamespacedName, &rtObj); err != nil {
		if apierrors.IsNotFound(err) {
			return releaseResult(r.releaseEverywhere(ctx, r.APIReader, r.Client, "HTTPRoute", req.Namespace, req.Name))
		}
		return ctrl.Result{}, err
	}
	emit := func(reason, msg string) { r.dedupe.emit(r.recorder, &rtObj, corev1.EventTypeWarning, reason, msg) }

	// Opt-in decision (design §2). The route's OWN towonel.io/tunnel is authoritative
	// when PRESENT: truthy → tunnel, false/garbage → release. Only an ABSENT key
	// inherits a parent Gateway's towonel.io/auto-routes default — check key
	// PRESENCE (not value), so an explicit "false" can never be force-tunneled.
	if raw, present := rtObj.Annotations[AnnotationTunnel]; present {
		if enabled, _ := ParseTruthy(raw); !enabled {
			return releaseResult(r.releaseEverywhere(ctx, r.APIReader, r.Client, "HTTPRoute", rtObj.Namespace, rtObj.Name))
		}
	} else {
		selected, serr := r.autoSelectedByGateway(ctx, &rtObj, emit)
		if serr != nil {
			return ctrl.Result{}, serr // transient — requeue
		}
		if !selected {
			return releaseResult(r.releaseEverywhere(ctx, r.APIReader, r.Client, "HTTPRoute", rtObj.Namespace, rtObj.Name))
		}
		// Exposure is never silent: record a Normal Event (the emit closure above is
		// Warning-typed, so call the recorder directly for the Normal type).
		r.dedupe.emit(r.recorder, &rtObj, corev1.EventTypeNormal, ReasonAutoSelectedByGateway,
			"auto-selected for tunneling by a parent Gateway's towonel.io/auto-routes; set towonel.io/tunnel: \"false\" on this route to opt out")
	}
	tunnel, ok, err := resolveTunnel(ctx, r.Client, emit, rtObj.Annotations[AnnotationTunnelRef], rtObj.Namespace)
	if err != nil {
		return ctrl.Result{}, err // transient List failure → requeue
	}
	if !ok {
		return ctrl.Result{}, nil // already emitted; no requeue
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

// parentRefNamespace returns the effective namespace a parentRef targets: an
// explicit p.Namespace, else the route's own namespace (Gateway API default).
func parentRefNamespace(p gwv1.ParentReference, routeNS string) string {
	if p.Namespace != nil {
		return string(*p.Namespace)
	}
	return routeNS
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
		ns := parentRefNamespace(p, rtObj.Namespace)
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

// routesForGateway enqueues HTTPRoutes affected by a change to the given Gateway:
// every annotated route whose parentRef targets it (so a gateway-service edit
// re-flows), plus every SAME-NAMESPACE route (annotated or not) so toggling the
// Gateway's towonel.io/auto-routes re-flows its un-annotated children (#25, §4.4).
// Cross-namespace un-annotated routes are skipped (never auto-selectable).
// Reconcile re-decides opt-in for each enqueued route.
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
		_, annotated := rt.Annotations[AnnotationTunnel]
		for _, p := range rt.Spec.ParentRefs {
			if !isGatewayParent(p) {
				continue
			}
			ns := parentRefNamespace(p, rt.Namespace)
			if ns != gw.Namespace || string(p.Name) != gw.Name {
				continue
			}
			// Enqueue if the route is explicitly annotated (apply/release as before)
			// OR it lives in the Gateway's OWN namespace — only same-namespace routes
			// can inherit towonel.io/auto-routes (#25, §2), so a cross-namespace
			// un-annotated route can never be selected and is skipped. Reconcile
			// re-decides opt-in for everything enqueued.
			if annotated || rt.Namespace == gw.Namespace {
				reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: rt.Namespace, Name: rt.Name}})
			}
			break
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
		For(&gwv1.HTTPRoute{}, builder.WithPredicates(httpRouteForPredicate())).
		Watches(&gwv1.Gateway{}, handler.EnqueueRequestsFromMapFunc(r.routesForGateway), builder.WithPredicates(crossWatchPredicate())).
		Watches(&towonelv1alpha1.TowonelAgent{}, handler.EnqueueRequestsFromMapFunc(r.sourcesForAgent), builder.WithPredicates(crossWatchPredicate())).
		Named("httproute-source").
		Complete(r)
}
