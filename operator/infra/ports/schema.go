package ports

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// SchemaRepository reads and writes KapeSchema resources.
type SchemaRepository interface {
	// Get fetches a KapeSchema by namespaced name. Returns nil, nil when not found.
	Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeSchema, error)

	// UpdateStatus persists status sub-resource changes.
	UpdateStatus(ctx context.Context, schema *v1alpha1.KapeSchema) error

	// AddFinalizer adds the given finalizer string to the schema if not already present.
	AddFinalizer(ctx context.Context, schema *v1alpha1.KapeSchema, finalizer string) error

	// RemoveFinalizer removes the given finalizer string from the schema.
	RemoveFinalizer(ctx context.Context, schema *v1alpha1.KapeSchema, finalizer string) error

	// ListHandlersBySchemaRef returns all KapeHandlers with label kape.io/schema-ref={schemaName}.
	ListHandlersBySchemaRef(ctx context.Context, schemaName string) ([]v1alpha1.KapeHandler, error)
}
