package ports

import (
	"context"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// SkillConfigMapPort manages the kape-skills-{handler-name} ConfigMap that
// holds one file per lazy KapeSkill instruction. The ConfigMap is created
// when at least one lazy skill is present and deleted when none remain.
type SkillConfigMapPort interface {
	// Ensure creates or patches the kape-skills-{handler-name} ConfigMap with
	// one data entry per lazy skill keyed as "{skill-name}.txt".
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, lazySkills []v1alpha1.KapeSkill) error

	// Delete removes the kape-skills-{handler-name} ConfigMap. Returns nil when
	// the ConfigMap does not exist.
	Delete(ctx context.Context, handler *v1alpha1.KapeHandler) error
}
