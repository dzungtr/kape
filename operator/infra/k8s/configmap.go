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

// ConfigMapAdapter implements ports.ConfigMapPort.
type ConfigMapAdapter struct {
	client client.Client
}

// NewConfigMapAdapter creates a new ConfigMapAdapter.
func NewConfigMapAdapter(c client.Client) *ConfigMapAdapter {
	return &ConfigMapAdapter{client: c}
}

// configMapName returns the conventional ConfigMap name for a handler.
func configMapName(handlerName string) string {
	return "kape-handler-" + handlerName
}

// Ensure creates or updates the settings.toml ConfigMap for the given handler.
func (a *ConfigMapAdapter) Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, tomlContent string) error {
	name := configMapName(handler.Name)
	key := types.NamespacedName{Name: name, Namespace: handler.Namespace}

	var cm corev1.ConfigMap
	err := a.client.Get(ctx, key, &cm)
	if apierrors.IsNotFound(err) {
		cm = buildConfigMap(handler, tomlContent)
		if err := a.client.Create(ctx, &cm); err != nil {
			return fmt.Errorf("creating ConfigMap %s/%s: %w", handler.Namespace, name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("getting ConfigMap %s/%s: %w", handler.Namespace, name, err)
	}

	// Update data if content changed.
	if cm.Data["settings.toml"] == tomlContent {
		return nil
	}
	patch := client.MergeFrom(cm.DeepCopy())
	cm.Data = map[string]string{"settings.toml": tomlContent}
	if err := a.client.Patch(ctx, &cm, patch); err != nil {
		return fmt.Errorf("patching ConfigMap %s/%s: %w", handler.Namespace, name, err)
	}
	return nil
}

func buildConfigMap(handler *v1alpha1.KapeHandler, tomlContent string) corev1.ConfigMap {
	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName(handler.Name),
			Namespace: handler.Namespace,
			Labels: map[string]string{
				"kape.io/handler": handler.Name,
				"app.kubernetes.io/managed-by": "kape-operator",
			},
		},
		Data: map[string]string{
			"settings.toml": tomlContent,
		},
	}
	// Owner reference: KapeHandler → ConfigMap (GC cascade on handler delete)
	setOwnerRef(handler, &cm.ObjectMeta)
	return cm
}

// setOwnerRef sets handler as the owner of the target ObjectMeta (same namespace required).
func setOwnerRef(handler *v1alpha1.KapeHandler, target *metav1.ObjectMeta) {
	isController := true
	blockOwnerDeletion := true
	target.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion:         "kape.io/v1alpha1",
			Kind:               "KapeHandler",
			Name:               handler.Name,
			UID:                handler.UID,
			Controller:         &isController,
			BlockOwnerDeletion: &blockOwnerDeletion,
		},
	}
}
