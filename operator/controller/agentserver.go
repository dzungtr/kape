package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// KapeAgentServerReconciler is the thin controller-runtime adapter for
// KapeAgentServer. All reconcile logic lives in reconcile.AgentServerReconciler.
type KapeAgentServerReconciler struct {
	inner *reconcilehandler.AgentServerReconciler
}

// NewKapeAgentServerReconciler creates a KapeAgentServerReconciler.
func NewKapeAgentServerReconciler(inner *reconcilehandler.AgentServerReconciler) *KapeAgentServerReconciler {
	return &KapeAgentServerReconciler{inner: inner}
}

// Reconcile implements reconcile.Reconciler.
func (r *KapeAgentServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.inner.Reconcile(ctx, req.NamespacedName)
}

// SetupAgentServerReconciler registers the KapeAgentServer reconciler. The
// owned watches make a deleted or edited rendered object re-converge without a
// CR edit (scenario "delete the controller Deployment → the operator recreates
// it").
func SetupAgentServerReconciler(mgr manager.Manager, inner *reconcilehandler.AgentServerReconciler, maxConcurrent int) error {
	r := NewKapeAgentServerReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeAgentServer{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&networkingv1.Ingress{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
