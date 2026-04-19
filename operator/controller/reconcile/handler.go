package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

// HandlerReconciler performs the full 12-step reconcile logic for KapeHandler.
type HandlerReconciler struct {
	handlers        ports.HandlerRepository
	schemas         ports.SchemaRepository
	tools           ports.ToolRepository
	configMaps      ports.ConfigMapPort
	serviceAccounts ports.ServiceAccountPort
	deployments     ports.DeploymentPort
	scaledObjects   ports.ScaledObjectPort
	tomlRenderer    ports.TOMLRenderer
	kapeConfig      ports.KapeConfigLoader
}

// NewHandlerReconciler creates a HandlerReconciler with all required dependencies.
func NewHandlerReconciler(
	handlers ports.HandlerRepository,
	schemas ports.SchemaRepository,
	tools ports.ToolRepository,
	configMaps ports.ConfigMapPort,
	serviceAccounts ports.ServiceAccountPort,
	deployments ports.DeploymentPort,
	scaledObjects ports.ScaledObjectPort,
	tomlRenderer ports.TOMLRenderer,
	kapeConfig ports.KapeConfigLoader,
) *HandlerReconciler {
	return &HandlerReconciler{
		handlers:        handlers,
		schemas:         schemas,
		tools:           tools,
		configMaps:      configMaps,
		serviceAccounts: serviceAccounts,
		deployments:     deployments,
		scaledObjects:   scaledObjects,
		tomlRenderer:    tomlRenderer,
		kapeConfig:      kapeConfig,
	}
}

// Reconcile implements the full 12-step KapeHandler reconcile loop.
func (r *HandlerReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("handler", key)

	// Step 1: Fetch
	handler, err := r.handlers.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeHandler: %w", err)
	}
	if handler == nil {
		return ctrl.Result{}, nil
	}

	// Step 2: Dependency gate
	schema, resolvedTools, depsReady, gateMsg, gateReason, err := r.validateDependencies(ctx, handler)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !depsReady {
		handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
			Type:    "DependenciesReady",
			Status:  metav1.ConditionFalse,
			Reason:  gateReason,
			Message: gateMsg,
		})
		handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
			Type:   "Ready",
			Status: metav1.ConditionFalse,
			Reason: "DependenciesNotReady",
		})
		_ = r.handlers.UpdateStatus(ctx, handler)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
		Type:   "DependenciesReady",
		Status: metav1.ConditionTrue,
		Reason: "Ready",
	})

	// Step 3: Validate scaling
	if handler.Spec.Scaling != nil && handler.Spec.Scaling.ScaleToZero && handler.Spec.Scaling.MinReplicas >= 1 {
		handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
			Type:    "ScalingConfigured",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidScalingConfig",
			Message: "scaleToZero: true requires minReplicas: 0",
		})
		_ = r.handlers.UpdateStatus(ctx, handler)
		return ctrl.Result{}, nil // terminal
	}

	// Step 4: Compute hashes
	rolloutHash, err := computeRolloutHash(handler, schema, resolvedTools)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("computing rollout hash: %w", err)
	}
	consumerName := strings.ReplaceAll(handler.Spec.Trigger.Type, ".", "-")

	// Step 5: Load config and render settings.toml
	cfg, err := r.kapeConfig.Load(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading kape-config: %w", err)
	}
	tomlContent, err := r.tomlRenderer.Render(handler, schema, resolvedTools, cfg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("rendering settings.toml: %w", err)
	}
	if err := r.configMaps.Ensure(ctx, handler, tomlContent); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ConfigMap: %w", err)
	}
	log.V(1).Info("ConfigMap reconciled")

	// Step 6: Ensure ServiceAccount
	if err := r.serviceAccounts.Ensure(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ServiceAccount: %w", err)
	}

	// Step 7: Ensure Deployment (with sidecar injection)
	if err := r.deployments.Ensure(ctx, handler, cfg, rolloutHash, resolvedTools); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring Deployment: %w", err)
	}
	log.V(1).Info("Deployment reconciled", "rolloutHash", rolloutHash)

	// Step 8: Ensure KEDA ScaledObject
	soKey := types.NamespacedName{Name: "kape-handler-" + handler.Name, Namespace: handler.Namespace}
	existingConsumer, soFound, err := r.scaledObjects.GetConsumerName(ctx, soKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading ScaledObject: %w", err)
	}
	if soFound && existingConsumer != consumerName {
		// trigger.type changed — delete and recreate
		if err := r.scaledObjects.Delete(ctx, soKey); err != nil {
			return ctrl.Result{}, fmt.Errorf("deleting stale ScaledObject: %w", err)
		}
	}
	if err := r.scaledObjects.Ensure(ctx, handler, consumerName, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ScaledObject: %w", err)
	}

	// Step 9: Sync labels
	labels := map[string]string{"kape.io/schema-ref": handler.Spec.SchemaRef}
	for _, t := range handler.Spec.Tools {
		labels["kape.io/tool-ref-"+t.Ref] = "true"
	}
	if err := r.handlers.SyncLabels(ctx, handler, labels); err != nil {
		log.Error(err, "failed to sync labels")
	}

	// Step 10: Refresh handler after label patch
	handler, err = r.handlers.Get(ctx, key)
	if err != nil || handler == nil {
		return ctrl.Result{}, err
	}

	// Step 11: Read Deployment status → build conditions
	depKey := types.NamespacedName{Name: "kape-handler-" + handler.Name, Namespace: handler.Namespace}
	depStatus, depFound, err := r.deployments.GetStatus(ctx, depKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading Deployment status: %w", err)
	}
	handler.Status.Conditions = buildHandlerConditions(depStatus, depFound, handler.Status.Conditions)
	if depFound && depStatus != nil {
		handler.Status.Replicas = depStatus.ReadyReplicas
	}

	// Step 12: Patch status
	if err := r.handlers.UpdateStatus(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

// validateDependencies checks KapeSchema + KapeTools readiness. Returns resolved objects on success.
func (r *HandlerReconciler) validateDependencies(ctx context.Context, handler *v1alpha1.KapeHandler) (
	schema *v1alpha1.KapeSchema,
	tools []v1alpha1.KapeTool,
	ready bool,
	message, reason string,
	err error,
) {
	// Check schema
	schemaKey := types.NamespacedName{Name: handler.Spec.SchemaRef, Namespace: handler.Namespace}
	schema, err = r.schemas.Get(ctx, schemaKey)
	if err != nil {
		return nil, nil, false, "", "", fmt.Errorf("fetching KapeSchema: %w", err)
	}
	if schema == nil || !isConditionTrue(schema.Status.Conditions, "Ready") {
		msg := fmt.Sprintf("KapeSchema %q not found or not ready", handler.Spec.SchemaRef)
		if schema != nil {
			if c := findCond(schema.Status.Conditions, "Ready"); c != nil {
				msg = c.Message
			}
		}
		return nil, nil, false, msg, "KapeSchemaInvalid", nil
	}

	// Check tools
	tools = make([]v1alpha1.KapeTool, 0, len(handler.Spec.Tools))
	for _, ref := range handler.Spec.Tools {
		toolKey := types.NamespacedName{Name: ref.Ref, Namespace: handler.Namespace}
		tool, err := r.tools.Get(ctx, toolKey)
		if err != nil {
			return nil, nil, false, "", "", fmt.Errorf("fetching KapeTool %q: %w", ref.Ref, err)
		}
		if tool == nil || !isConditionTrue(tool.Status.Conditions, "Ready") {
			msg := fmt.Sprintf("KapeTool %q not found or not ready", ref.Ref)
			if tool != nil {
				if c := findCond(tool.Status.Conditions, "Ready"); c != nil {
					msg = fmt.Sprintf("KapeTool %q: %s", ref.Ref, c.Message)
				}
			}
			return nil, nil, false, msg, "KapeToolNotReady", nil
		}
		tools = append(tools, *tool)
	}
	return schema, tools, true, "", "", nil
}

func computeRolloutHash(handler *v1alpha1.KapeHandler, schema *v1alpha1.KapeSchema, tools []v1alpha1.KapeTool) (string, error) {
	h := sha256.New()
	for _, item := range []interface{}{handler.Spec, schema.Spec} {
		b, err := json.Marshal(item)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	for _, t := range tools {
		b, err := json.Marshal(t.Spec)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func buildHandlerConditions(depStatus *appsv1.DeploymentStatus, depFound bool, existing []metav1.Condition) []metav1.Condition {
	deploymentAvailable := metav1.Condition{Type: "DeploymentAvailable"}
	ready := metav1.Condition{Type: "Ready"}

	if !depFound {
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "DeploymentNotFound"
		ready.Status = metav1.ConditionFalse
		ready.Reason = "DeploymentNotFound"
	} else if depStatus == nil || depStatus.ReadyReplicas == 0 {
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "MinimumReplicasUnavailable"
		ready.Status = metav1.ConditionFalse
		ready.Reason = "DeploymentUnavailable"
	} else {
		deploymentAvailable.Status = metav1.ConditionTrue
		deploymentAvailable.Reason = "Available"
		deploymentAvailable.Message = fmt.Sprintf("%d/%d replicas ready", depStatus.ReadyReplicas, depStatus.Replicas)
		ready.Status = metav1.ConditionTrue
		ready.Reason = "Ready"
	}

	existing = setCondition(existing, deploymentAvailable)
	existing = setCondition(existing, ready)
	return existing
}

func isConditionTrue(conditions []metav1.Condition, condType string) bool {
	c := findCond(conditions, condType)
	return c != nil && c.Status == metav1.ConditionTrue
}

func findCond(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
