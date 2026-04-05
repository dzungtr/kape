// Package reconcile contains the full reconcile logic for KapeHandler.
package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

// HandlerReconciler performs the full Phase 2 reconcile logic for KapeHandler.
// It depends only on ports interfaces so it is testable without a live cluster.
type HandlerReconciler struct {
	handlers        ports.HandlerRepository
	configMaps      ports.ConfigMapPort
	serviceAccounts ports.ServiceAccountPort
	deployments     ports.DeploymentPort
	tomlRenderer    ports.TOMLRenderer
	kapeConfig      ports.KapeConfigLoader
}

// New creates a HandlerReconciler with all required dependencies.
func New(
	handlers ports.HandlerRepository,
	configMaps ports.ConfigMapPort,
	serviceAccounts ports.ServiceAccountPort,
	deployments ports.DeploymentPort,
	tomlRenderer ports.TOMLRenderer,
	kapeConfig ports.KapeConfigLoader,
) *HandlerReconciler {
	return &HandlerReconciler{
		handlers:        handlers,
		configMaps:      configMaps,
		serviceAccounts: serviceAccounts,
		deployments:     deployments,
		tomlRenderer:    tomlRenderer,
		kapeConfig:      kapeConfig,
	}
}

// Reconcile implements the Phase 2 KapeHandler reconcile loop.
func (r *HandlerReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("handler", key)

	// 1. Fetch
	handler, err := r.handlers.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeHandler: %w", err)
	}
	if handler == nil {
		return ctrl.Result{}, nil // deleted; GC handles owned resources
	}

	// 2. Load platform config
	cfg, err := r.kapeConfig.Load(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading kape-config: %w", err)
	}

	// 3. Compute rollout hash from spec
	rolloutHash, err := computeRolloutHash(handler.Spec)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("computing rollout hash: %w", err)
	}

	// 4. Render settings.toml
	tomlContent, err := r.tomlRenderer.Render(handler, cfg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("rendering settings.toml: %w", err)
	}

	// 5. Ensure ConfigMap
	if err := r.configMaps.Ensure(ctx, handler, tomlContent); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ConfigMap: %w", err)
	}
	log.V(1).Info("ConfigMap reconciled")

	// 6. Ensure ServiceAccount
	if err := r.serviceAccounts.Ensure(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ServiceAccount: %w", err)
	}

	// 7. Ensure Deployment
	if err := r.deployments.Ensure(ctx, handler, cfg, rolloutHash); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring Deployment: %w", err)
	}
	log.V(1).Info("Deployment reconciled", "rolloutHash", rolloutHash)

	// 8. Sync labels onto KapeHandler for cross-resource watches
	labels := map[string]string{
		"kape.io/schema-ref": handler.Spec.SchemaRef,
	}
	for _, t := range handler.Spec.Tools {
		labels["kape.io/tool-ref-"+t.Ref] = "true"
	}
	if err := r.handlers.SyncLabels(ctx, handler, labels); err != nil {
		// Non-fatal; log and continue so status still updates.
		log.Error(err, "failed to sync labels")
	}

	// 9. Refresh handler after label patch (UID/ResourceVersion may have changed)
	handler, err = r.handlers.Get(ctx, key)
	if err != nil || handler == nil {
		return ctrl.Result{}, err
	}

	// 10. Read Deployment status and update conditions
	depKey := types.NamespacedName{
		Name:      "kape-handler-" + handler.Name,
		Namespace: handler.Namespace,
	}
	depStatus, depFound, err := r.deployments.GetStatus(ctx, depKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading Deployment status: %w", err)
	}
	handler.Status.Conditions = buildConditions(depStatus, depFound, handler.Status.Conditions)
	if depFound && depStatus != nil {
		handler.Status.Replicas = depStatus.ReadyReplicas
	}

	// 11. Patch status sub-resource (RetryOnConflict is handled inside the repository)
	if err := r.handlers.UpdateStatus(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

// computeRolloutHash returns a stable sha256 hex hash of the KapeHandlerSpec.
func computeRolloutHash(spec v1alpha1.KapeHandlerSpec) (string, error) {
	b, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

// buildConditions constructs status.conditions from the Deployment status.
func buildConditions(depStatus *appsv1.DeploymentStatus, depFound bool, existing []metav1.Condition) []metav1.Condition {
	now := metav1.Now()

	deploymentAvailable := metav1.Condition{
		Type:               "DeploymentAvailable",
		LastTransitionTime: now,
	}
	ready := metav1.Condition{
		Type:               "Ready",
		LastTransitionTime: now,
	}

	if !depFound {
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "DeploymentNotFound"
		deploymentAvailable.Message = "Deployment has not been created yet"
		ready.Status = metav1.ConditionFalse
		ready.Reason = "DeploymentNotFound"
		ready.Message = "Deployment does not exist"
	} else if depStatus == nil || depStatus.ReadyReplicas == 0 {
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "MinimumReplicasUnavailable"
		deploymentAvailable.Message = "No ready replicas"
		ready.Status = metav1.ConditionFalse
		ready.Reason = "DeploymentUnavailable"
		ready.Message = "Handler deployment has no ready replicas"
	} else {
		deploymentAvailable.Status = metav1.ConditionTrue
		deploymentAvailable.Reason = "Available"
		deploymentAvailable.Message = fmt.Sprintf("%d/%d replicas ready", depStatus.ReadyReplicas, depStatus.Replicas)
		ready.Status = metav1.ConditionTrue
		ready.Reason = "Ready"
		ready.Message = "Handler is ready"
	}

	// Preserve lastTransitionTime for conditions that haven't changed.
	for _, c := range existing {
		switch c.Type {
		case "DeploymentAvailable":
			if c.Status == deploymentAvailable.Status {
				deploymentAvailable.LastTransitionTime = c.LastTransitionTime
			}
		case "Ready":
			if c.Status == ready.Status {
				ready.LastTransitionTime = c.LastTransitionTime
			}
		}
	}

	return []metav1.Condition{deploymentAvailable, ready}
}
