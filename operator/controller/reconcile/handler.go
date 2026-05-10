package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

// Reason constants for the DependenciesReady condition. The Ready rollup
// (see buildHandlerConditions) is the negative form: Ready=True iff no
// condition is explicitly False, which is forward-compatible with the
// KapeProxyReady condition slice 6 will introduce.
const (
	ReasonReady             = "Ready"
	ReasonKapeSchemaInvalid = "KapeSchemaInvalid"
	ReasonKapeToolNotReady  = "KapeToolNotReady"
	ReasonKapeSkillNotFound = "KapeSkillNotFound"
	ReasonKapeSkillNotReady = "KapeSkillNotReady"
)

// resolvedDependencies is the carrier between dependency resolution and the
// downstream reconcile steps (rollout hash, deployment, system prompt, lazy
// ConfigMap, label sync). Per spec §2.1 the contract is:
//
//   - Schema:  the Ready KapeSchema referenced by handler.spec.schemaRef
//   - Tools:   sorted slice of every KapeTool in the unioned toolMap
//     (by KapeTool.Name) — used for deterministic hashing and
//     settings.toml [tools.*] section emission
//   - Skills:  every KapeSkill from handler.spec.skills[] in declaration
//     order — used for hash (D13) and system prompt assembly
//   - ToolMap: keyed by KapeTool.Name (D13); union of handler-direct tools
//     and skill-pulled tools; downstream consumers iterate Tools
//     for deterministic order, ToolMap for O(1) lookup
type resolvedDependencies struct {
	Schema  *v1alpha1.KapeSchema
	Tools   []v1alpha1.KapeTool
	Skills  []v1alpha1.KapeSkill
	ToolMap map[string]v1alpha1.KapeTool
}

// EagerSkills returns skills with LazyLoad=false in declaration order.
func (d *resolvedDependencies) EagerSkills() []v1alpha1.KapeSkill {
	out := make([]v1alpha1.KapeSkill, 0, len(d.Skills))
	for _, s := range d.Skills {
		if !s.Spec.LazyLoad {
			out = append(out, s)
		}
	}
	return out
}

// LazySkills returns skills with LazyLoad=true in declaration order.
func (d *resolvedDependencies) LazySkills() []v1alpha1.KapeSkill {
	out := make([]v1alpha1.KapeSkill, 0, len(d.Skills))
	for _, s := range d.Skills {
		if s.Spec.LazyLoad {
			out = append(out, s)
		}
	}
	return out
}

// HandlerReconciler performs the full reconcile logic for KapeHandler.
type HandlerReconciler struct {
	handlers        ports.HandlerRepository
	schemas         ports.SchemaRepository
	tools           ports.ToolRepository
	skills          ports.SkillRepository
	configMaps      ports.ConfigMapPort
	skillConfigMaps ports.SkillConfigMapPort
	serviceAccounts ports.ServiceAccountPort
	deployments     ports.DeploymentPort
	scaledObjects   ports.ScaledObjectPort
	tomlRenderer    ports.TOMLRenderer
	kapeConfig      ports.KapeConfigLoader
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
	skillConfigMaps ports.SkillConfigMapPort,
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
		skills:          skills,
		configMaps:      configMaps,
		skillConfigMaps: skillConfigMaps,
		serviceAccounts: serviceAccounts,
		deployments:     deployments,
		scaledObjects:   scaledObjects,
		tomlRenderer:    tomlRenderer,
		kapeConfig:      kapeConfig,
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
	log := ctrl.LoggerFrom(ctx).WithValues("handler", key)

	// 1. Fetch
	handler, err := r.handlers.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeHandler: %w", err)
	}
	if handler == nil {
		return ctrl.Result{}, nil
	}

	// 2. Dependency gate
	deps, depsReady, gateMsg, gateReason, err := r.validateDependencies(ctx, handler)
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
		// Ready rollup for the early-exit case: set explicitly here.
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
		Reason: ReasonReady,
	})

	// 3. Validate scaling
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

	// 4. Compute hashes
	rolloutHash, err := computeRolloutHash(handler, deps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("computing rollout hash: %w", err)
	}
	consumerName := strings.ReplaceAll(handler.Spec.Trigger.Type, ".", "-")

	// 5. Load config and render settings.toml
	cfg, err := r.kapeConfig.Load(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading kape-config: %w", err)
	}
	eagerSkills := deps.EagerSkills()
	lazySkills := deps.LazySkills()
	tomlContent, err := r.tomlRenderer.Render(handler, deps.Schema, deps.Tools, eagerSkills, lazySkills, cfg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("rendering settings.toml: %w", err)
	}
	if err := r.configMaps.Ensure(ctx, handler, tomlContent); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ConfigMap: %w", err)
	}
	log.V(1).Info("ConfigMap reconciled")

	// 6. Reconcile lazy-skills ConfigMap (create when lazy skills exist, delete otherwise)
	lazySkillsPresent := len(lazySkills) > 0
	if lazySkillsPresent {
		if err := r.skillConfigMaps.Ensure(ctx, handler, lazySkills); err != nil {
			return ctrl.Result{}, fmt.Errorf("ensuring kape-skills ConfigMap: %w", err)
		}
	} else {
		if err := r.skillConfigMaps.Delete(ctx, handler); err != nil {
			return ctrl.Result{}, fmt.Errorf("deleting kape-skills ConfigMap: %w", err)
		}
	}

	// 7. Ensure ServiceAccount
	if err := r.serviceAccounts.Ensure(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ServiceAccount: %w", err)
	}

	// 8. Ensure Deployment (mounts /etc/kape/skills when lazy skills present)
	if err := r.deployments.Ensure(ctx, handler, cfg, rolloutHash, deps.Tools, lazySkillsPresent); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring Deployment: %w", err)
	}
	log.V(1).Info("Deployment reconciled", "rolloutHash", rolloutHash)

	// 9. Ensure KEDA ScaledObject
	soKey := types.NamespacedName{Name: handlerScaledObjectName(handler), Namespace: handler.Namespace}
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

	// 10. Sync labels
	//
	// Per spec D7+D8:
	//   - kape.io/schema-ref={name}
	//   - kape.io/tool-ref-{name}=true for EVERY tool in deps.Tools
	//     (handler-direct + skill-pulled — transitive)
	//   - kape.io/skill-ref-{name}=true for every entry in handler.spec.skills[]
	//
	// Slice 4 reads kape.io/skill-ref-* to enqueue handler reconciles when a
	// referenced KapeSkill changes. This slice produces the labels only.
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

	// 11. Refresh handler after label patch
	handler, err = r.handlers.Get(ctx, key)
	if err != nil || handler == nil {
		return ctrl.Result{}, err
	}

	// 12. Read Deployment status → build conditions
	depKey := types.NamespacedName{Name: handlerDeploymentName(handler), Namespace: handler.Namespace}
	depStatus, depFound, err := r.deployments.GetStatus(ctx, depKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading Deployment status: %w", err)
	}
	handler.Status.Conditions = buildHandlerConditions(depStatus, depFound, handler.Status.Conditions)
	if depFound && depStatus != nil {
		handler.Status.Replicas = depStatus.ReadyReplicas
	}

	// 13. Patch status
	if err := r.handlers.UpdateStatus(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

// unionToolMap inserts a tool into the map keyed by tool.Name. Subsequent
// inserts of the same name are no-ops per spec D13 (KapeTool name uniqueness
// makes overwrite semantically equivalent).
func unionToolMap(m map[string]v1alpha1.KapeTool, tool v1alpha1.KapeTool) {
	if _, ok := m[tool.Name]; ok {
		return
	}
	m[tool.Name] = tool
}

// sortedToolsByName returns the values of a toolMap as a slice sorted by Name.
// Sorting is required for hash stability per spec §2.1.
func sortedToolsByName(m map[string]v1alpha1.KapeTool) []v1alpha1.KapeTool {
	out := make([]v1alpha1.KapeTool, 0, len(m))
	for _, t := range m {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// validateDependencies checks KapeSchema + KapeTool + KapeSkill readiness.
// Returns a fully resolved resolvedDependencies on success. On any not-ready
// dependency, returns ready=false with reason from the constants block.
//
// Population order per spec §2.1:
//
//  1. handler.spec.tools[]   → toolMap[tool.Name] = tool
//  2. handler.spec.skills[]  → fetch each KapeSkill, gate on Ready
//  3. each skill.spec.tools[] → fetch each KapeTool, gate on Ready,
//     union into toolMap
//  4. sort toolMap values by Name → deps.Tools (for hash stability)
//
// Skills slice keeps declaration order (NOT sorted) per D13.
func (r *HandlerReconciler) validateDependencies(ctx context.Context, handler *v1alpha1.KapeHandler) (
	deps *resolvedDependencies,
	ready bool,
	message, reason string,
	err error,
) {
	// (1) Schema
	schemaKey := types.NamespacedName{Name: handler.Spec.SchemaRef, Namespace: handler.Namespace}
	schema, err := r.schemas.Get(ctx, schemaKey)
	if err != nil {
		return nil, false, "", "", fmt.Errorf("fetching KapeSchema: %w", err)
	}
	if schema == nil || !isConditionTrue(schema.Status.Conditions, "Ready") {
		msg := fmt.Sprintf("KapeSchema %q not found or not ready", handler.Spec.SchemaRef)
		if schema != nil {
			if c := findCond(schema.Status.Conditions, "Ready"); c != nil && c.Message != "" {
				msg = c.Message
			}
		}
		return nil, false, msg, ReasonKapeSchemaInvalid, nil
	}

	toolMap := make(map[string]v1alpha1.KapeTool)

	// (2) Handler-direct tools
	for _, ref := range handler.Spec.Tools {
		toolKey := types.NamespacedName{Name: ref.Ref, Namespace: handler.Namespace}
		tool, err := r.tools.Get(ctx, toolKey)
		if err != nil {
			return nil, false, "", "", fmt.Errorf("fetching KapeTool %q: %w", ref.Ref, err)
		}
		if tool == nil || !isConditionTrue(tool.Status.Conditions, "Ready") {
			msg := fmt.Sprintf("KapeTool %q not found or not ready", ref.Ref)
			if tool != nil {
				if c := findCond(tool.Status.Conditions, "Ready"); c != nil && c.Message != "" {
					msg = fmt.Sprintf("KapeTool %q: %s", ref.Ref, c.Message)
				}
			}
			return nil, false, msg, ReasonKapeToolNotReady, nil
		}
		unionToolMap(toolMap, *tool)
	}

	// (3) Skills + skill-pulled tools
	skillsList := make([]v1alpha1.KapeSkill, 0, len(handler.Spec.Skills))
	for _, ref := range handler.Spec.Skills {
		skillKey := types.NamespacedName{Name: ref.Ref, Namespace: handler.Namespace}
		skill, err := r.skills.Get(ctx, skillKey)
		if err != nil {
			return nil, false, "", "", fmt.Errorf("fetching KapeSkill %q: %w", ref.Ref, err)
		}
		if skill == nil {
			return nil, false, fmt.Sprintf("KapeSkill %q not found", ref.Ref), ReasonKapeSkillNotFound, nil
		}
		if !isConditionTrue(skill.Status.Conditions, "Ready") {
			msg := fmt.Sprintf("KapeSkill %q not ready", ref.Ref)
			if c := findCond(skill.Status.Conditions, "Ready"); c != nil && c.Message != "" {
				msg = fmt.Sprintf("KapeSkill %q: %s", ref.Ref, c.Message)
			}
			return nil, false, msg, ReasonKapeSkillNotReady, nil
		}

		// Skill-pulled tools
		for _, sToolRef := range skill.Spec.Tools {
			toolKey := types.NamespacedName{Name: sToolRef.Ref, Namespace: handler.Namespace}
			tool, err := r.tools.Get(ctx, toolKey)
			if err != nil {
				return nil, false, "", "", fmt.Errorf("fetching KapeTool %q (via skill %q): %w", sToolRef.Ref, ref.Ref, err)
			}
			if tool == nil || !isConditionTrue(tool.Status.Conditions, "Ready") {
				msg := fmt.Sprintf("KapeSkill %q: KapeTool %q not Ready", ref.Ref, sToolRef.Ref)
				return nil, false, msg, ReasonKapeSkillNotReady, nil
			}
			unionToolMap(toolMap, *tool)
		}
		skillsList = append(skillsList, *skill)
	}

	deps = &resolvedDependencies{
		Schema:  schema,
		Tools:   sortedToolsByName(toolMap),
		Skills:  skillsList,
		ToolMap: toolMap,
	}
	return deps, true, "", "", nil
}

// computeRolloutHash hashes (in this fixed order, per spec §2.1):
//
//  1. handler.Spec
//  2. schema.Spec
//  3. for each tool in deps.Tools (sorted by Name): tool.Spec
//  4. for each skill in deps.Skills (declaration order): skill.Spec
//
// Skills are NOT sorted (D13): reordering handler.spec.skills[] changes the
// system prompt assembly order, so the hash must reflect order, not just
// set membership.
func computeRolloutHash(handler *v1alpha1.KapeHandler, deps *resolvedDependencies) (string, error) {
	h := sha256.New()
	for _, item := range []interface{}{handler.Spec, deps.Schema.Spec} {
		b, err := json.Marshal(item)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	for _, t := range deps.Tools {
		b, err := json.Marshal(t.Spec)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	for _, s := range deps.Skills {
		b, err := json.Marshal(s.Spec)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// buildHandlerConditions computes DeploymentAvailable from the Deployment
// status and then folds the entire condition set into the Ready rollup.
//
// Per spec §2.3 the rollup is the NEGATIVE form: Ready=True iff no condition
// in the slice is explicitly False. This is forward-compatible with future
// owners (slice 6's KapeProxyReady, etc.) without changes here.
func buildHandlerConditions(depStatus *appsv1.DeploymentStatus, depFound bool, existing []metav1.Condition) []metav1.Condition {
	deploymentAvailable := metav1.Condition{Type: "DeploymentAvailable"}

	switch {
	case !depFound:
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "DeploymentNotFound"
	case depStatus == nil || depStatus.ReadyReplicas == 0:
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "MinimumReplicasUnavailable"
	default:
		deploymentAvailable.Status = metav1.ConditionTrue
		deploymentAvailable.Reason = "Available"
		deploymentAvailable.Message = fmt.Sprintf("%d/%d replicas ready", depStatus.ReadyReplicas, depStatus.Replicas)
	}
	existing = setCondition(existing, deploymentAvailable)
	existing = setCondition(existing, computeReadyRollup(existing))
	return existing
}

// computeReadyRollup folds every condition in the slice (except "Ready"
// itself) into the Ready rollup using the negative form: Ready=True iff no
// condition is explicitly False. Per spec §2.3 forward-compat rule.
//
// Reason precedence on False: first False condition wins (deterministic via
// slice order). Reason on True is "Ready".
func computeReadyRollup(conditions []metav1.Condition) metav1.Condition {
	for _, c := range conditions {
		if c.Type == "Ready" {
			continue
		}
		if c.Status == metav1.ConditionFalse {
			return metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  c.Reason,
				Message: c.Message,
			}
		}
	}
	return metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: ReasonReady}
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
