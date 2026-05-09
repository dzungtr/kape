package reconcile

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

const skillFinalizer = "kape.io/skill-protection"

// SkillReconciler performs the full reconcile logic for KapeSkill.
type SkillReconciler struct {
	skills ports.SkillRepository
	tools  ports.ToolRepository
}

// NewSkillReconciler creates a SkillReconciler.
func NewSkillReconciler(skills ports.SkillRepository, tools ports.ToolRepository) *SkillReconciler {
	return &SkillReconciler{skills: skills, tools: tools}
}

// Reconcile implements the KapeSkill reconcile loop.
func (r *SkillReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	skill, err := r.skills.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeSkill: %w", err)
	}
	if skill == nil {
		return ctrl.Result{}, nil
	}

	// 1. Validate spec fields
	if err := validateSkillSpec(skill); err != nil {
		skill.Status.Conditions = setCondition(skill.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidSpec",
			Message: err.Error(),
		})
		_ = r.skills.UpdateStatus(ctx, skill)
		return ctrl.Result{}, nil // terminal
	}

	// 2. Manage finalizer
	if err := r.skills.AddFinalizer(ctx, skill, skillFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
	}

	// 3. Handle deletion
	if !skill.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, skill)
	}

	// 4. Resolve each referenced KapeTool
	for _, ref := range skill.Spec.Tools {
		toolKey := types.NamespacedName{Name: ref.Ref, Namespace: skill.Namespace}
		tool, err := r.tools.Get(ctx, toolKey)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("fetching KapeTool %q: %w", ref.Ref, err)
		}
		if tool == nil {
			skill.Status.Conditions = setCondition(skill.Status.Conditions, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "KapeToolNotFound",
				Message: fmt.Sprintf("KapeTool %q not found", ref.Ref),
			})
			_ = r.skills.UpdateStatus(ctx, skill)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		if !isReady(tool.Status.Conditions) {
			skill.Status.Conditions = setCondition(skill.Status.Conditions, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "KapeToolNotReady",
				Message: fmt.Sprintf("KapeTool %q is not Ready", ref.Ref),
			})
			_ = r.skills.UpdateStatus(ctx, skill)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// 5. All tools ready (or no tools) — set Ready=True
	skill.Status.Conditions = setCondition(skill.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Ready",
		Message: "All referenced tools are ready",
	})
	if err := r.skills.UpdateStatus(ctx, skill); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SkillReconciler) handleDeletion(ctx context.Context, skill *v1alpha1.KapeSkill) (ctrl.Result, error) {
	handlers, err := r.skills.ListHandlersBySkillRef(ctx, skill.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing handlers for skill %q: %w", skill.Name, err)
	}
	if len(handlers) > 0 {
		names := make([]string, 0, len(handlers))
		for _, h := range handlers {
			names = append(names, h.Name)
		}
		skill.Status.Conditions = setCondition(skill.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ReferencedByHandlers",
			Message: fmt.Sprintf("Cannot delete: referenced by handlers: [%s]", strings.Join(names, ", ")),
		})
		_ = r.skills.UpdateStatus(ctx, skill)
		return ctrl.Result{}, nil // blocked — re-triggered on handler deletion
	}
	if err := r.skills.RemoveFinalizer(ctx, skill, skillFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func validateSkillSpec(skill *v1alpha1.KapeSkill) error {
	if skill.Spec.Description == "" {
		return fmt.Errorf("spec.description must not be empty")
	}
	if skill.Spec.Instruction == "" {
		return fmt.Errorf("spec.instruction must not be empty")
	}
	return nil
}

// isReady returns true when a Ready=True condition is present in the condition slice.
func isReady(conditions []metav1.Condition) bool {
	for _, c := range conditions {
		if c.Type == "Ready" && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}
