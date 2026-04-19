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

// SchemaRepository implements ports.SchemaRepository.
type SchemaRepository struct {
	client client.Client
}

// NewSchemaRepository creates a new SchemaRepository.
func NewSchemaRepository(c client.Client) *SchemaRepository {
	return &SchemaRepository{client: c}
}

// Get fetches a KapeSchema by namespaced name. Returns nil, nil when not found.
func (r *SchemaRepository) Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeSchema, error) {
	var schema v1alpha1.KapeSchema
	if err := r.client.Get(ctx, key, &schema); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return &schema, nil
}

// UpdateStatus persists the schema's status sub-resource using RetryOnConflict.
func (r *SchemaRepository) UpdateStatus(ctx context.Context, schema *v1alpha1.KapeSchema) error {
	key := types.NamespacedName{Name: schema.Name, Namespace: schema.Namespace}
	desired := schema.Status
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest v1alpha1.KapeSchema
		if err := r.client.Get(ctx, key, &latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		latest.Status = desired
		return r.client.Status().Update(ctx, &latest)
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("updating KapeSchema %s/%s status: %w", schema.Namespace, schema.Name, err)
	}
	return nil
}

// AddFinalizer adds the given finalizer to the schema if not already present.
func (r *SchemaRepository) AddFinalizer(ctx context.Context, schema *v1alpha1.KapeSchema, finalizer string) error {
	if controllerutil.ContainsFinalizer(schema, finalizer) {
		return nil
	}
	patch := client.MergeFrom(schema.DeepCopy())
	controllerutil.AddFinalizer(schema, finalizer)
	if err := r.client.Patch(ctx, schema, patch); err != nil {
		return fmt.Errorf("adding finalizer to KapeSchema %s/%s: %w", schema.Namespace, schema.Name, err)
	}
	return nil
}

// RemoveFinalizer removes the given finalizer from the schema.
func (r *SchemaRepository) RemoveFinalizer(ctx context.Context, schema *v1alpha1.KapeSchema, finalizer string) error {
	if !controllerutil.ContainsFinalizer(schema, finalizer) {
		return nil
	}
	patch := client.MergeFrom(schema.DeepCopy())
	controllerutil.RemoveFinalizer(schema, finalizer)
	if err := r.client.Patch(ctx, schema, patch); err != nil {
		return fmt.Errorf("removing finalizer from KapeSchema %s/%s: %w", schema.Namespace, schema.Name, err)
	}
	return nil
}

// ListHandlersBySchemaRef returns KapeHandlers with label kape.io/schema-ref={schemaName}.
func (r *SchemaRepository) ListHandlersBySchemaRef(ctx context.Context, schemaName string) ([]v1alpha1.KapeHandler, error) {
	var list v1alpha1.KapeHandlerList
	if err := r.client.List(ctx, &list, client.MatchingLabels{
		"kape.io/schema-ref": schemaName,
	}); err != nil {
		return nil, fmt.Errorf("listing handlers by schema ref %q: %w", schemaName, err)
	}
	return list.Items, nil
}
