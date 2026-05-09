package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

// KapeSkillReconciler is the thin controller-runtime adapter for KapeSkill.
type KapeSkillReconciler struct {
	inner *reconcile.SkillReconciler
}

// NewKapeSkillReconciler creates a KapeSkillReconciler.
func NewKapeSkillReconciler(inner *reconcile.SkillReconciler) *KapeSkillReconciler {
	return &KapeSkillReconciler{inner: inner}
}

// Reconcile implements reconcile.Reconciler.
func (r *KapeSkillReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.inner.Reconcile(ctx, req.NamespacedName)
}

// SetupSkillReconciler registers the KapeSkill reconciler with the controller manager.
func SetupSkillReconciler(mgr manager.Manager, inner *reconcile.SkillReconciler, maxConcurrent int) error {
	r := NewKapeSkillReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeSkill{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
