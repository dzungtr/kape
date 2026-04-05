// Package controller contains thin controller-runtime reconcilers that delegate to reconcile/.
package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
)

// KapeHandlerReconciler is the thin controller-runtime adapter for KapeHandler.
// All reconcile logic lives in reconcile.HandlerReconciler.
type KapeHandlerReconciler struct {
	inner *reconcilehandler.HandlerReconciler
}

// NewKapeHandlerReconciler creates a KapeHandlerReconciler.
func NewKapeHandlerReconciler(inner *reconcilehandler.HandlerReconciler) *KapeHandlerReconciler {
	return &KapeHandlerReconciler{inner: inner}
}

// Reconcile implements reconcile.Reconciler.
func (r *KapeHandlerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.inner.Reconcile(ctx, req.NamespacedName)
}

// SetupHandlerReconciler registers the KapeHandler reconciler with the controller manager.
// It watches owned Deployments, ConfigMaps, and ServiceAccounts so that changes to them
// re-enqueue the owning KapeHandler.
func SetupHandlerReconciler(mgr manager.Manager, inner *reconcilehandler.HandlerReconciler, maxConcurrent int) error {
	r := NewKapeHandlerReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeHandler{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrent,
		}).
		Complete(r)
}
