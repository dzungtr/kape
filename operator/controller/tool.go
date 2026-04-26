package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

// KapeToolReconciler is the thin controller-runtime adapter for KapeTool.
type KapeToolReconciler struct {
	inner *reconcile.ToolReconciler
}

// NewKapeToolReconciler creates a KapeToolReconciler.
func NewKapeToolReconciler(inner *reconcile.ToolReconciler) *KapeToolReconciler {
	return &KapeToolReconciler{inner: inner}
}

// Reconcile implements reconcile.Reconciler.
func (r *KapeToolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.inner.Reconcile(ctx, req.NamespacedName)
}

// SetupToolReconciler registers the KapeTool reconciler with the controller manager.
func SetupToolReconciler(mgr manager.Manager, inner *reconcile.ToolReconciler, maxConcurrent int) error {
	r := NewKapeToolReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeTool{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
