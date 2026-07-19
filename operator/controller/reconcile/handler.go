package reconcile

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

// HandlerReconciler performs the full reconcile logic for KapeHandler.
type HandlerReconciler struct {
	handlers         ports.HandlerRepository
	schemas          ports.SchemaRepository
	tools            ports.ToolRepository
	skills           ports.SkillRepository
	configMaps       ports.ConfigMapPort
	kapeproxyConfigs ports.KapeproxyConfigPort
	skillConfigMaps  ports.SkillConfigMapPort
	serviceAccounts  ports.ServiceAccountPort
	deployments      ports.DeploymentPort
	scaledObjects    ports.ScaledObjectPort
	tomlRenderer     ports.TOMLRenderer
	kapeConfig       ports.KapeConfigLoader
}

func handlerDeploymentName(handler *v1alpha1.KapeHandler) string {
	return "kape-handler-" + handler.Name
}

func handlerScaledObjectName(handler *v1alpha1.KapeHandler) string {
	return "kape-handler-" + handler.Name
}

// NewHandlerReconciler creates a HandlerReconciler with all required dependencies.
func NewHandlerReconciler(
	handlers ports.HandlerRepository,
	schemas ports.SchemaRepository,
	tools ports.ToolRepository,
	skills ports.SkillRepository,
	configMaps ports.ConfigMapPort,
	kapeproxyConfigs ports.KapeproxyConfigPort,
	skillConfigMaps ports.SkillConfigMapPort,
	serviceAccounts ports.ServiceAccountPort,
	deployments ports.DeploymentPort,
	scaledObjects ports.ScaledObjectPort,
	tomlRenderer ports.TOMLRenderer,
	kapeConfig ports.KapeConfigLoader,
) *HandlerReconciler {
	return &HandlerReconciler{
		handlers:         handlers,
		schemas:          schemas,
		tools:            tools,
		skills:           skills,
		configMaps:       configMaps,
		kapeproxyConfigs: kapeproxyConfigs,
		skillConfigMaps:  skillConfigMaps,
		serviceAccounts:  serviceAccounts,
		deployments:      deployments,
		scaledObjects:    scaledObjects,
		tomlRenderer:     tomlRenderer,
		kapeConfig:       kapeConfig,
	}
}

// Reconcile implements the full KapeHandler reconcile loop:
//
//  1. Fetch KapeHandler
//  2. Validate dependencies (schema + tools + skills + skill-pulled tools)
//  3. Validate scaling
//  4. Compute rollout hash (handler + schema + sorted tools + ordered skills)
//  5. Render settings.toml + ensure ConfigMap (settings)
//  6. Reconcile lazy-skills ConfigMap (create when lazy skills exist; delete when none)
//  7. Ensure ServiceAccount
//  8. Ensure Deployment (mounts /etc/kape/skills when lazy skills present)
//  9. Ensure KEDA ScaledObject
//  10. Sync labels (schema-ref + tool-ref-{name} for unioned tools + skill-ref-{name})
//  11. Refresh handler after label patch
//  12. Read Deployment status → build conditions
//  13. Patch status
func (r *HandlerReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	handler, err := r.handlers.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeHandler: %w", err)
	}
	if handler == nil {
		return ctrl.Result{}, nil
	}

	deps, depsReady, gateMsg, gateReason, err := r.validateDependencies(ctx, handler)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !depsReady {
		return r.gateDependencies(ctx, handler, gateReason, gateMsg)
	}
	handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
		Type:   "DependenciesReady",
		Status: metav1.ConditionTrue,
		Reason: ReasonReady,
	})

	if done, result, err := r.gateScaling(ctx, handler); done {
		return result, err
	}

	cfg, err := r.kapeConfig.Load(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading kape-config: %w", err)
	}

	rolloutHash, err := computeRolloutHash(handler, deps, cfg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("computing rollout hash: %w", err)
	}

	if err := r.reconcileWorkload(ctx, handler, deps, cfg, rolloutHash); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.syncLabels(ctx, handler, deps); err != nil {
		return ctrl.Result{}, err
	}

	handler, err = r.handlers.Get(ctx, key)
	if err != nil || handler == nil {
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, handler); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

// gateDependencies records the DependenciesReady=False / Ready=False conditions
// for an unready handler and requeues. Status update errors are swallowed: the
// next requeue retries.
func (r *HandlerReconciler) gateDependencies(ctx context.Context, handler *v1alpha1.KapeHandler, reason, message string) (ctrl.Result, error) {
	handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
		Type:    "DependenciesReady",
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionFalse,
		Reason: "DependenciesNotReady",
	})
	_ = r.handlers.UpdateStatus(ctx, handler)
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// gateScaling validates the scaling config. An invalid scaleToZero/minReplicas
// combination is terminal: it records ScalingConfigured=False and returns
// done=true so Reconcile stops without requeueing.
func (r *HandlerReconciler) gateScaling(ctx context.Context, handler *v1alpha1.KapeHandler) (done bool, result ctrl.Result, err error) {
	if handler.Spec.Scaling != nil && handler.Spec.Scaling.ScaleToZero && handler.Spec.Scaling.MinReplicas >= 1 {
		handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
			Type:    "ScalingConfigured",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidScalingConfig",
			Message: "scaleToZero: true requires minReplicas: 0",
		})
		_ = r.handlers.UpdateStatus(ctx, handler)
		return true, ctrl.Result{}, nil
	}
	return false, ctrl.Result{}, nil
}

// reconcileWorkload renders config and materialises every owned resource:
// settings + kapeproxy ConfigMaps, the lazy-skills ConfigMap lifecycle, the
// ServiceAccount, the Deployment, and the KEDA ScaledObject.
func (r *HandlerReconciler) reconcileWorkload(ctx context.Context, handler *v1alpha1.KapeHandler, deps *resolvedDependencies, cfg domainconfig.KapeConfig, rolloutHash string) error {
	log := ctrl.LoggerFrom(ctx).WithValues("handler", handler.Name)

	eagerSkills := deps.EagerSkills()
	lazySkills := deps.LazySkills()

	tomlContent, err := r.tomlRenderer.Render(handler, deps.Schema, deps.Tools, eagerSkills, lazySkills, cfg)
	if err != nil {
		return fmt.Errorf("rendering settings.toml: %w", err)
	}
	if err := r.configMaps.Ensure(ctx, handler, tomlContent); err != nil {
		return fmt.Errorf("ensuring ConfigMap: %w", err)
	}
	log.V(1).Info("ConfigMap reconciled")

	var mcpTools []v1alpha1.KapeTool
	for _, t := range deps.Tools {
		if t.Spec.Type == "mcp" {
			mcpTools = append(mcpTools, t)
		}
	}
	if err := r.kapeproxyConfigs.Ensure(ctx, handler, mcpTools); err != nil {
		return fmt.Errorf("ensuring kapeproxy-config ConfigMap: %w", err)
	}
	log.V(1).Info("kapeproxy-config ConfigMap reconciled")

	lazySkillsPresent := len(lazySkills) > 0
	if lazySkillsPresent {
		if err := r.skillConfigMaps.Ensure(ctx, handler, lazySkills); err != nil {
			return fmt.Errorf("ensuring kape-skills ConfigMap: %w", err)
		}
	} else {
		if err := r.skillConfigMaps.Delete(ctx, handler); err != nil {
			return fmt.Errorf("deleting kape-skills ConfigMap: %w", err)
		}
	}

	if err := r.serviceAccounts.Ensure(ctx, handler); err != nil {
		return fmt.Errorf("ensuring ServiceAccount: %w", err)
	}

	if err := r.deployments.Ensure(ctx, handler, cfg, rolloutHash, deps.Tools, lazySkillsPresent); err != nil {
		return fmt.Errorf("ensuring Deployment: %w", err)
	}
	log.V(1).Info("Deployment reconciled", "rolloutHash", rolloutHash)

	return r.reconcileScaledObject(ctx, handler, cfg)
}

// reconcileScaledObject ensures the KEDA ScaledObject, deleting and recreating
// it when the handler's trigger.type (and thus consumer name) has changed.
func (r *HandlerReconciler) reconcileScaledObject(ctx context.Context, handler *v1alpha1.KapeHandler, cfg domainconfig.KapeConfig) error {
	consumerName := strings.ReplaceAll(handler.Spec.Trigger.Type, ".", "-")
	soKey := types.NamespacedName{Name: handlerScaledObjectName(handler), Namespace: handler.Namespace}
	existingConsumer, soFound, err := r.scaledObjects.GetConsumerName(ctx, soKey)
	if err != nil {
		return fmt.Errorf("reading ScaledObject: %w", err)
	}
	if soFound && existingConsumer != consumerName {
		if err := r.scaledObjects.Delete(ctx, soKey); err != nil {
			return fmt.Errorf("deleting stale ScaledObject: %w", err)
		}
	}
	if err := r.scaledObjects.Ensure(ctx, handler, consumerName, cfg); err != nil {
		return fmt.Errorf("ensuring ScaledObject: %w", err)
	}
	return nil
}

// syncLabels writes the ref labels for cross-resource enqueue (spec D7+D8):
// schema-ref, tool-ref-{name} for every unioned tool (handler-direct +
// skill-pulled), and skill-ref-{name} for each entry in handler.spec.skills[].
// Patch failures are logged, not fatal — labels are reconstructed next pass.
func (r *HandlerReconciler) syncLabels(ctx context.Context, handler *v1alpha1.KapeHandler, deps *resolvedDependencies) error {
	log := ctrl.LoggerFrom(ctx).WithValues("handler", handler.Name)
	labels := map[string]string{"kape.io/schema-ref": handler.Spec.SchemaRef}
	for _, t := range deps.Tools {
		labels["kape.io/tool-ref-"+t.Name] = "true"
	}
	for _, s := range handler.Spec.Skills {
		labels["kape.io/skill-ref-"+s.Ref] = "true"
	}
	if err := r.handlers.SyncLabels(ctx, handler, labels); err != nil {
		log.Error(err, "failed to sync labels")
	}
	return nil
}

// updateStatus reads the Deployment status, folds it into the handler's
// condition set and replica count, and patches the handler status subresource.
func (r *HandlerReconciler) updateStatus(ctx context.Context, handler *v1alpha1.KapeHandler) error {
	depKey := types.NamespacedName{Name: handlerDeploymentName(handler), Namespace: handler.Namespace}
	depStatus, depFound, err := r.deployments.GetStatus(ctx, depKey)
	if err != nil {
		return fmt.Errorf("reading Deployment status: %w", err)
	}
	handler.Status.Conditions = buildHandlerConditions(depStatus, depFound, handler.Status.Conditions)
	if depFound && depStatus != nil {
		handler.Status.Replicas = depStatus.ReadyReplicas
	}
	if err := r.handlers.UpdateStatus(ctx, handler); err != nil {
		return fmt.Errorf("updating status: %w", err)
	}
	return nil
}
