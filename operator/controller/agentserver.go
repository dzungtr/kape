package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
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
//
// The Secret watch closes the rotation loop (ADR-0088): editing a credential
// Secret re-enqueues every KapeAgentServer in its namespace that references it,
// and the reconcile re-pushes the provider record. Only Secrets labelled
// kape.io/credential=true are watched.
func SetupAgentServerReconciler(mgr manager.Manager, inner *reconcilehandler.AgentServerReconciler, maxConcurrent int) error {
	r := NewKapeAgentServerReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeAgentServer{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&networkingv1.Ingress{}).
		Watches(&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(MapSecretToAgentServers(mgr.GetClient())),
			builder.WithPredicates(CredentialSecretPredicate())).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
