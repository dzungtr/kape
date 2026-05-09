package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// SkillConfigMapAdapter implements ports.SkillConfigMapPort.
type SkillConfigMapAdapter struct {
	client client.Client
}

// NewSkillConfigMapAdapter creates a SkillConfigMapAdapter.
func NewSkillConfigMapAdapter(c client.Client) *SkillConfigMapAdapter {
	return &SkillConfigMapAdapter{client: c}
}

// SkillConfigMapName is the name of the kape-skills ConfigMap for a handler.
func SkillConfigMapName(handlerName string) string {
	return "kape-skills-" + handlerName
}

// Ensure creates or patches the kape-skills-{handler-name} ConfigMap.
// Each lazy skill becomes one data entry keyed "{skill-name}.txt".
func (a *SkillConfigMapAdapter) Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, lazySkills []v1alpha1.KapeSkill) error {
	name := SkillConfigMapName(handler.Name)
	key := types.NamespacedName{Name: name, Namespace: handler.Namespace}

	data := make(map[string]string, len(lazySkills))
	for _, s := range lazySkills {
		data[s.Name+".txt"] = s.Spec.Instruction
	}

	desired := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: handler.Namespace,
			Labels: map[string]string{
				"kape.io/handler":              handler.Name,
				"app.kubernetes.io/managed-by": "kape-operator",
				"app.kubernetes.io/name":       name,
			},
		},
		Data: data,
	}
	setOwnerRef(handler, &desired.ObjectMeta)

	var existing corev1.ConfigMap
	err := a.client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		return a.client.Create(ctx, &desired)
	}
	if err != nil {
		return fmt.Errorf("getting ConfigMap %s/%s: %w", handler.Namespace, name, err)
	}

	patch := client.MergeFrom(existing.DeepCopy())
	existing.Data = desired.Data
	existing.Labels = desired.Labels
	existing.OwnerReferences = desired.OwnerReferences
	return a.client.Patch(ctx, &existing, patch)
}

// Delete removes the kape-skills-{handler-name} ConfigMap. Returns nil if not found.
func (a *SkillConfigMapAdapter) Delete(ctx context.Context, handler *v1alpha1.KapeHandler) error {
	name := SkillConfigMapName(handler.Name)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: handler.Namespace},
	}
	if err := a.client.Delete(ctx, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("deleting ConfigMap %s/%s: %w", handler.Namespace, name, err)
	}
	return nil
}
