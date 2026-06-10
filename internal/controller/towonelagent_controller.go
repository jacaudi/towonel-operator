package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	record "k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	towonelv1alpha1 "github.com/jacaudi/towonel-operator/api/v1alpha1"
)

// TowonelAgentReconciler reconciles a TowonelAgent object.
type TowonelAgentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=towonel.io,resources=towonelagents,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=towonel.io,resources=towonelagents/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=towonel.io,resources=towonelagents/finalizers,verbs=update
//+kubebuilder:rbac:groups="apps",resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

// Reconcile is a no-op placeholder; agent logic lands in a later phase.
func (r *TowonelAgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = ctx
	_ = req
	return ctrl.Result{}, nil
}

// SetupWithManager wires the reconciler to the manager.
func (r *TowonelAgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&towonelv1alpha1.TowonelAgent{}).
		Named("towonelagent").
		Complete(r)
}
