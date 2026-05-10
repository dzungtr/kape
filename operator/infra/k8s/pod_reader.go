package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodReaderAdapter implements ports.PodReader using a controller-runtime client.
type PodReaderAdapter struct {
	client client.Client
}

// NewPodReaderAdapter creates a PodReaderAdapter.
func NewPodReaderAdapter(c client.Client) *PodReaderAdapter {
	return &PodReaderAdapter{client: c}
}

// GetPod fetches a single Pod by namespaced name. Returns nil, nil when not found.
func (a *PodReaderAdapter) GetPod(ctx context.Context, key types.NamespacedName) (*corev1.Pod, error) {
	var pod corev1.Pod
	if err := a.client.Get(ctx, key, &pod); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return &pod, nil
}

// ListByHandlerName returns all Pods in namespace with label kape.io/handler=<name>.
func (a *PodReaderAdapter) ListByHandlerName(ctx context.Context, namespace, handlerName string) ([]corev1.Pod, error) {
	var list corev1.PodList
	if err := a.client.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{"kape.io/handler": handlerName},
	); err != nil {
		return nil, fmt.Errorf("listing pods for handler %s/%s: %w", namespace, handlerName, err)
	}
	return list.Items, nil
}
