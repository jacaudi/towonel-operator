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
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// HTTPRouteSourceReconciler forwards a route's hostnames directly to its single
// backend Service (design §4.1, direct-to-backend).
type HTTPRouteSourceReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	AgentNamespace string
	sourceBase
}

//+kubebuilder:rbac:groups="gateway.networking.k8s.io",resources=httproutes,verbs=get;list;watch
//+kubebuilder:rbac:groups="gateway.networking.k8s.io",resources=referencegrants,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=towonel.io,resources=towonelagents,verbs=get;list;watch;create;update;patch;delete

func (r *HTTPRouteSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.ensure(r.Recorder)
	var rt_obj gwv1.HTTPRoute
	if err := r.Get(ctx, req.NamespacedName, &rt_obj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.releaseEverywhere(ctx, r.Client, "HTTPRoute", req.Namespace, req.Name)
		}
		return ctrl.Result{}, err
	}
	if enabled, _ := ParseTruthy(rt_obj.Annotations[AnnotationTunnel]); !enabled {
		return ctrl.Result{}, r.releaseEverywhere(ctx, r.Client, "HTTPRoute", rt_obj.Namespace, rt_obj.Name)
	}
	emit := func(reason, msg string) { r.dedupe.emit(r.recorder, &rt_obj, corev1.EventTypeWarning, reason, msg) }
	tunnel, err := parseTunnelRef(rt_obj.Annotations[AnnotationTunnelRef], rt_obj.Namespace)
	if err != nil {
		emit(ReasonTunnelRefMissing, err.Error())
		return ctrl.Result{}, nil
	}
	rt, ok, derr := r.deriveHTTPRouteRouting(ctx, &rt_obj, emit)
	if derr != nil {
		return ctrl.Result{}, derr // transient (e.g. ReferenceGrant list failed) — requeue
	}
	if !ok {
		return ctrl.Result{}, nil
	}
	return r.applyContribution(ctx, r.Client, r.AgentNamespace, "HTTPRoute", &rt_obj, tunnel, rt_obj.Annotations[AnnotationAgentRef], rt)
}

type backendRef struct {
	ns, name string
	port     int32
}

// deriveHTTPRouteRouting returns (routing, ok, error). A non-nil error is
// transient (a List/Get failure) and is threaded up so Reconcile requeues; ok
// is false WITH a nil error when an Event was emitted (not retryable).
func (r *HTTPRouteSourceReconciler) deriveHTTPRouteRouting(ctx context.Context, rt_obj *gwv1.HTTPRoute, emit func(string, string)) (routing, bool, error) {
	seen := map[backendRef]struct{}{}
	for _, rule := range rt_obj.Spec.Rules {
		for _, b := range rule.BackendRefs {
			if b.Kind != nil && *b.Kind != "Service" {
				emit(ReasonInvalidAnnotation, fmt.Sprintf("backendRef kind %q is not Service; skipped", *b.Kind))
				continue
			}
			ns := rt_obj.Namespace
			if b.Namespace != nil {
				ns = string(*b.Namespace)
			}
			var port int32
			if b.Port != nil {
				port = int32(*b.Port)
			}
			seen[backendRef{ns: ns, name: string(b.Name), port: port}] = struct{}{}
		}
	}
	if len(seen) == 0 {
		emit(ReasonInvalidAnnotation, "HTTPRoute has no Service backendRef to use as an origin")
		return routing{}, false, nil
	}
	if len(seen) > 1 {
		emit(ReasonAmbiguousBackend, "HTTPRoute resolves to multiple distinct Service backends; declare a TowonelAgent directly or split the route")
		return routing{}, false, nil
	}
	var only backendRef
	for b := range seen {
		only = b
	}
	if only.ns != rt_obj.Namespace {
		allowed, err := referenceGrantAllows(ctx, r.Client, only.ns, rt_obj.Namespace, only.name)
		if err != nil {
			return routing{}, false, err // transient — requeue
		}
		if !allowed {
			emit(ReasonBackendRefDenied, fmt.Sprintf("cross-namespace backendRef to %s/%s requires a ReferenceGrant in %q", only.ns, only.name, only.ns))
			return routing{}, false, nil
		}
	}
	var svc corev1.Service
	if err := r.Get(ctx, types.NamespacedName{Namespace: only.ns, Name: only.name}, &svc); err != nil {
		if apierrors.IsNotFound(err) {
			emit(ReasonInvalidAnnotation, fmt.Sprintf("backend Service %s/%s not found", only.ns, only.name))
			return routing{}, false, nil
		}
		return routing{}, false, err // transient — requeue
	}
	port := only.port
	if port == 0 {
		if len(svc.Spec.Ports) == 0 {
			emit(ReasonInvalidAnnotation, fmt.Sprintf("backend Service %s/%s has no ports", only.ns, only.name))
			return routing{}, false, nil
		}
		port = svc.Spec.Ports[0].Port
	}
	origin := originOf(svc.Spec.ClusterIP, port)
	var out routing
	for _, h := range rt_obj.Spec.Hostnames {
		out.services = append(out.services, map[string]any{"hostname": string(h), "origin": origin})
	}
	if out.empty() {
		emit(ReasonInvalidAnnotation, "HTTPRoute has no spec.hostnames to expose")
		return routing{}, false, nil
	}
	return out, true, nil
}

// referenceGrantAllows reports whether a ReferenceGrant in backendNS permits an
// HTTPRoute in routeNS to reference Service backendName.
func referenceGrantAllows(ctx context.Context, c client.Client, backendNS, routeNS, backendName string) (bool, error) {
	var grants gwv1beta1.ReferenceGrantList
	if err := c.List(ctx, &grants, client.InNamespace(backendNS)); err != nil {
		return false, err
	}
	for i := range grants.Items {
		g := &grants.Items[i]
		fromOK := false
		for _, f := range g.Spec.From {
			if f.Group == "gateway.networking.k8s.io" && f.Kind == "HTTPRoute" && string(f.Namespace) == routeNS {
				fromOK = true
				break
			}
		}
		if !fromOK {
			continue
		}
		for _, to := range g.Spec.To {
			if to.Group != "" || to.Kind != "Service" {
				continue
			}
			if to.Name == nil || string(*to.Name) == backendName {
				return true, nil
			}
		}
	}
	return false, nil
}

func (r *HTTPRouteSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gwv1.HTTPRoute{}).
		Named("httproute-source").
		Complete(r)
}
