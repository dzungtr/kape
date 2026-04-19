package ports

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// ToolRepository reads and writes KapeTool resources.
type ToolRepository interface {
	// Get fetches a KapeTool by namespaced name. Returns nil, nil when not found.
	Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeTool, error)

	// UpdateStatus persists status sub-resource changes.
	UpdateStatus(ctx context.Context, tool *v1alpha1.KapeTool) error

	// ListHandlersByToolRef returns all KapeHandlers with label kape.io/tool-ref-{toolName}=true.
	ListHandlersByToolRef(ctx context.Context, toolName string) ([]v1alpha1.KapeHandler, error)
}

// StatefulSetPort manages the Qdrant StatefulSet and headless Service for memory-type KapeTools.
type StatefulSetPort interface {
	// EnsureQdrant creates or patches the Qdrant StatefulSet and headless Service.
	EnsureQdrant(ctx context.Context, tool *v1alpha1.KapeTool, cfg domainconfig.KapeConfig) error

	// GetQdrantReadyReplicas returns the number of ready replicas. found=false when StatefulSet does not exist.
	GetQdrantReadyReplicas(ctx context.Context, key types.NamespacedName) (readyReplicas int32, found bool, err error)
}

// ScaledObjectPort manages KEDA ScaledObject resources for KapeHandlers.
type ScaledObjectPort interface {
	// Ensure creates or patches the KEDA ScaledObject for the handler.
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, consumerName string, cfg domainconfig.KapeConfig) error

	// GetConsumerName reads the NATS consumer name from the existing ScaledObject.
	// found=false when the ScaledObject does not exist.
	GetConsumerName(ctx context.Context, key types.NamespacedName) (consumerName string, found bool, err error)

	// Delete removes the ScaledObject. Returns nil when not found.
	Delete(ctx context.Context, key types.NamespacedName) error
}
