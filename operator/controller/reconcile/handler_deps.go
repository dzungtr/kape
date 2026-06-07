package reconcile

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// Reason constants for the DependenciesReady condition. The Ready rollup
// (see computeReadyRollup) is the negative form: Ready=True iff no
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
	schema, ok, msg, reason, err := r.resolveSchema(ctx, handler)
	if err != nil || !ok {
		return nil, ok, msg, reason, err
	}

	toolMap := make(map[string]v1alpha1.KapeTool)

	// Handler-direct tools
	for _, ref := range handler.Spec.Tools {
		tool, ok, msg, reason, err := r.resolveHandlerTool(ctx, handler.Namespace, ref.Ref)
		if err != nil || !ok {
			return nil, ok, msg, reason, err
		}
		unionToolMap(toolMap, *tool)
	}

	// Skills + skill-pulled tools
	skillsList, ok, msg, reason, err := r.resolveSkills(ctx, handler, toolMap)
	if err != nil || !ok {
		return nil, ok, msg, reason, err
	}

	deps = &resolvedDependencies{
		Schema:  schema,
		Tools:   sortedToolsByName(toolMap),
		Skills:  skillsList,
		ToolMap: toolMap,
	}
	return deps, true, "", "", nil
}

// resolveSchema fetches the handler's KapeSchema and gates on its Ready
// condition. On a missing or not-Ready schema it returns ok=false with the
// KapeSchemaInvalid reason and the schema's Ready message when available.
func (r *HandlerReconciler) resolveSchema(ctx context.Context, handler *v1alpha1.KapeHandler) (
	schema *v1alpha1.KapeSchema, ok bool, message, reason string, err error,
) {
	schemaKey := types.NamespacedName{Name: handler.Spec.SchemaRef, Namespace: handler.Namespace}
	schema, err = r.schemas.Get(ctx, schemaKey)
	if err != nil {
		return nil, false, "", "", fmt.Errorf("fetching KapeSchema: %w", err)
	}
	if schema == nil || !meta.IsStatusConditionTrue(schema.Status.Conditions, "Ready") {
		msg := fmt.Sprintf("KapeSchema %q not found or not ready", handler.Spec.SchemaRef)
		if schema != nil {
			if c := meta.FindStatusCondition(schema.Status.Conditions, "Ready"); c != nil && c.Message != "" {
				msg = c.Message
			}
		}
		return nil, false, msg, ReasonKapeSchemaInvalid, nil
	}
	return schema, true, "", "", nil
}

// resolveHandlerTool fetches a handler-direct KapeTool and gates on its Ready
// condition. On a missing or not-Ready tool it returns ok=false with the
// KapeToolNotReady reason.
func (r *HandlerReconciler) resolveHandlerTool(ctx context.Context, namespace, ref string) (
	tool *v1alpha1.KapeTool, ok bool, message, reason string, err error,
) {
	toolKey := types.NamespacedName{Name: ref, Namespace: namespace}
	tool, err = r.tools.Get(ctx, toolKey)
	if err != nil {
		return nil, false, "", "", fmt.Errorf("fetching KapeTool %q: %w", ref, err)
	}
	if tool == nil || !meta.IsStatusConditionTrue(tool.Status.Conditions, "Ready") {
		msg := fmt.Sprintf("KapeTool %q not found or not ready", ref)
		if tool != nil {
			if c := meta.FindStatusCondition(tool.Status.Conditions, "Ready"); c != nil && c.Message != "" {
				msg = fmt.Sprintf("KapeTool %q: %s", ref, c.Message)
			}
		}
		return nil, false, msg, ReasonKapeToolNotReady, nil
	}
	return tool, true, "", "", nil
}

// resolveSkills fetches each KapeSkill in handler.spec.skills[] (declaration
// order), gates on Ready, and unions every skill-pulled KapeTool into toolMap.
// Returns the resolved skills in declaration order per D13.
func (r *HandlerReconciler) resolveSkills(ctx context.Context, handler *v1alpha1.KapeHandler, toolMap map[string]v1alpha1.KapeTool) (
	skills []v1alpha1.KapeSkill, ok bool, message, reason string, err error,
) {
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
		if !meta.IsStatusConditionTrue(skill.Status.Conditions, "Ready") {
			msg := fmt.Sprintf("KapeSkill %q not ready", ref.Ref)
			if c := meta.FindStatusCondition(skill.Status.Conditions, "Ready"); c != nil && c.Message != "" {
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
			if tool == nil || !meta.IsStatusConditionTrue(tool.Status.Conditions, "Ready") {
				msg := fmt.Sprintf("KapeSkill %q: KapeTool %q not Ready", ref.Ref, sToolRef.Ref)
				return nil, false, msg, ReasonKapeSkillNotReady, nil
			}
			unionToolMap(toolMap, *tool)
		}
		skillsList = append(skillsList, *skill)
	}
	return skillsList, true, "", "", nil
}
