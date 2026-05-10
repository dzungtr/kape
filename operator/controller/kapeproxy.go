package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
)

// KapeProxyController is the thin controller-runtime adapter for the KapeProxyReconciler.
type KapeProxyController struct {
	inner *reconcilehandler.KapeProxyReconciler
}

// NewKapeProxyController creates a KapeProxyController.
func NewKapeProxyController(inner *reconcilehandler.KapeProxyReconciler) *KapeProxyController {
	return &KapeProxyController{inner: inner}
}

// Reconcile implements reconcile.Reconciler — delegates to the inner reconciler.
func (r *KapeProxyController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.inner.Reconcile(ctx, req.NamespacedName)
}

// SetupKapeProxyReconciler registers the KapeProxy controller with the manager.
// It watches Pods with label kape.io/handler=* and enqueues them directly.
func SetupKapeProxyReconciler(mgr manager.Manager, inner *reconcilehandler.KapeProxyReconciler, maxConcurrent int) error {
	r := NewKapeProxyController(inner)
	return ctrl.NewControllerManagedBy(mgr).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(mapHandlerPods)).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}

// mapHandlerPods enqueues a reconcile request for each Pod that carries kape.io/handler label.
// Pods without the label are silently ignored.
func mapHandlerPods(_ context.Context, obj client.Object) []reconcile.Request {
	labels := obj.GetLabels()
	if labels == nil {
		return nil
	}
	if _, ok := labels["kape.io/handler"]; !ok {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}},
	}
}
