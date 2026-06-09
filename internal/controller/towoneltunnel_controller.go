// Package controller holds the towonel-operator reconcilers.
package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// TowonelTunnelReconciler reconciles a TowonelTunnel object.
type TowonelTunnelReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=towonel.io,resources=towoneltunnels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=towonel.io,resources=towoneltunnels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=towonel.io,resources=towoneltunnels/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is a no-op placeholder; tunnel logic lands in a later phase.
func (r *TowonelTunnelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = ctx
	_ = req
	return ctrl.Result{}, nil
}

// SetupWithManager wires the reconciler to the manager.
func (r *TowonelTunnelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&towonelv1alpha1.TowonelTunnel{}).
		Named("towoneltunnel").
		Complete(r)
}
