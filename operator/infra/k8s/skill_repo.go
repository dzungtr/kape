package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// SkillRepository implements ports.SkillRepository.
type SkillRepository struct {
	client client.Client
}

// NewSkillRepository creates a new SkillRepository.
func NewSkillRepository(c client.Client) *SkillRepository {
	return &SkillRepository{client: c}
}

// Get fetches a KapeSkill by namespaced name. Returns nil, nil when not found.
func (r *SkillRepository) Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeSkill, error) {
	var skill v1alpha1.KapeSkill
	if err := r.client.Get(ctx, key, &skill); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return &skill, nil
}

// UpdateStatus persists the skill's status sub-resource using RetryOnConflict.
func (r *SkillRepository) UpdateStatus(ctx context.Context, skill *v1alpha1.KapeSkill) error {
	key := types.NamespacedName{Name: skill.Name, Namespace: skill.Namespace}
	desired := skill.Status
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest v1alpha1.KapeSkill
		if err := r.client.Get(ctx, key, &latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		latest.Status = desired
		return r.client.Status().Update(ctx, &latest)
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("updating KapeSkill %s/%s status: %w", skill.Namespace, skill.Name, err)
	}
	return nil
}

// AddFinalizer adds the given finalizer to the skill if not already present.
func (r *SkillRepository) AddFinalizer(ctx context.Context, skill *v1alpha1.KapeSkill, finalizer string) error {
	if controllerutil.ContainsFinalizer(skill, finalizer) {
		return nil
	}
	patch := client.MergeFrom(skill.DeepCopy())
	controllerutil.AddFinalizer(skill, finalizer)
	if err := r.client.Patch(ctx, skill, patch); err != nil {
		return fmt.Errorf("adding finalizer to KapeSkill %s/%s: %w", skill.Namespace, skill.Name, err)
	}
	return nil
}

// RemoveFinalizer removes the given finalizer from the skill.
func (r *SkillRepository) RemoveFinalizer(ctx context.Context, skill *v1alpha1.KapeSkill, finalizer string) error {
	if !controllerutil.ContainsFinalizer(skill, finalizer) {
		return nil
	}
	patch := client.MergeFrom(skill.DeepCopy())
	controllerutil.RemoveFinalizer(skill, finalizer)
	if err := r.client.Patch(ctx, skill, patch); err != nil {
		return fmt.Errorf("removing finalizer from KapeSkill %s/%s: %w", skill.Namespace, skill.Name, err)
	}
	return nil
}

// ListHandlersBySkillRef returns KapeHandlers with label kape.io/skill-ref-{skillName}=true.
func (r *SkillRepository) ListHandlersBySkillRef(ctx context.Context, skillName string) ([]v1alpha1.KapeHandler, error) {
	var list v1alpha1.KapeHandlerList
	if err := r.client.List(ctx, &list, client.MatchingLabels{
		"kape.io/skill-ref-" + skillName: "true",
	}); err != nil {
		return nil, fmt.Errorf("listing handlers by skill ref %q: %w", skillName, err)
	}
	return list.Items, nil
}
