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

	// AddFinalizer adds the given finalizer string to the tool if not already present.
	AddFinalizer(ctx context.Context, tool *v1alpha1.KapeTool, finalizer string) error

	// RemoveFinalizer removes the given finalizer string from the tool.
	RemoveFinalizer(ctx context.Context, tool *v1alpha1.KapeTool, finalizer string) error

	// ListHandlersByToolRef returns all KapeHandlers with label kape.io/tool-ref-{toolName}=true.
	ListHandlersByToolRef(ctx context.Context, toolName string) ([]v1alpha1.KapeHandler, error)
}

// QdrantClusterPort delegates Qdrant provisioning to an upstream database operator
// by creating/updating a QdrantCluster CRD and polling its status conditions.
type QdrantClusterPort interface {
	// IsCRDInstalled returns true when QdrantCluster.qdrant.io is registered in the cluster.
	IsCRDInstalled(ctx context.Context) (bool, error)

	// EnsureQdrantCluster creates or updates a QdrantCluster owned by the KapeTool.
	EnsureQdrantCluster(ctx context.Context, tool *v1alpha1.KapeTool) error

	// GetQdrantClusterStatus returns readiness and the connection URL from the upstream CRD status.
	// found=false when the QdrantCluster does not exist yet.
	GetQdrantClusterStatus(ctx context.Context, key types.NamespacedName) (ready bool, connectionURL string, found bool, err error)
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
