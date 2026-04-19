package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

// KapeSchemaReconciler is the thin controller-runtime adapter for KapeSchema.
type KapeSchemaReconciler struct {
	inner *reconcile.SchemaReconciler
}

// NewKapeSchemaReconciler creates a KapeSchemaReconciler.
func NewKapeSchemaReconciler(inner *reconcile.SchemaReconciler) *KapeSchemaReconciler {
	return &KapeSchemaReconciler{inner: inner}
}

// Reconcile implements reconcile.Reconciler.
func (r *KapeSchemaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.inner.Reconcile(ctx, req.NamespacedName)
}

// SetupSchemaReconciler registers the KapeSchema reconciler with the controller manager.
func SetupSchemaReconciler(mgr manager.Manager, inner *reconcile.SchemaReconciler, maxConcurrent int) error {
	r := NewKapeSchemaReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeSchema{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
