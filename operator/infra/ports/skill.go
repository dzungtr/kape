package ports

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// SkillRepository reads and writes KapeSkill resources.
type SkillRepository interface {
	// Get fetches a KapeSkill by namespaced name. Returns nil, nil when not found.
	Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeSkill, error)

	// UpdateStatus persists status sub-resource changes.
	UpdateStatus(ctx context.Context, skill *v1alpha1.KapeSkill) error

	// AddFinalizer adds the given finalizer string to the skill if not already present.
	AddFinalizer(ctx context.Context, skill *v1alpha1.KapeSkill, finalizer string) error

	// RemoveFinalizer removes the given finalizer string from the skill.
	RemoveFinalizer(ctx context.Context, skill *v1alpha1.KapeSkill, finalizer string) error

	// ListHandlersBySkillRef returns all KapeHandlers with label kape.io/skill-ref-{skillName}=true.
	ListHandlersBySkillRef(ctx context.Context, skillName string) ([]v1alpha1.KapeHandler, error)
}
