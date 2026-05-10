package ports

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// PodReader provides read-only access to Pod resources.
// Implementations must never mutate Pods.
type PodReader interface {
	// GetPod fetches a single Pod by namespaced name. Returns nil, nil when not found.
	GetPod(ctx context.Context, key types.NamespacedName) (*corev1.Pod, error)

	// ListByHandlerName returns all Pods in the given namespace with label kape.io/handler=<name>.
	ListByHandlerName(ctx context.Context, namespace, handlerName string) ([]corev1.Pod, error)
}
