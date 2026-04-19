package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// ToolRepository implements ports.ToolRepository.
type ToolRepository struct {
	client client.Client
}

// NewToolRepository creates a new ToolRepository.
func NewToolRepository(c client.Client) *ToolRepository {
	return &ToolRepository{client: c}
}

// Get fetches a KapeTool by namespaced name. Returns nil, nil when not found.
func (r *ToolRepository) Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeTool, error) {
	var tool v1alpha1.KapeTool
	if err := r.client.Get(ctx, key, &tool); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return &tool, nil
}

// UpdateStatus persists the tool's status sub-resource using RetryOnConflict.
func (r *ToolRepository) UpdateStatus(ctx context.Context, tool *v1alpha1.KapeTool) error {
	key := types.NamespacedName{Name: tool.Name, Namespace: tool.Namespace}
	desired := tool.Status
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest v1alpha1.KapeTool
		if err := r.client.Get(ctx, key, &latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		latest.Status = desired
		return r.client.Status().Update(ctx, &latest)
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("updating KapeTool %s/%s status: %w", tool.Namespace, tool.Name, err)
	}
	return nil
}

// ListHandlersByToolRef returns KapeHandlers with label kape.io/tool-ref-{toolName}=true.
func (r *ToolRepository) ListHandlersByToolRef(ctx context.Context, toolName string) ([]v1alpha1.KapeHandler, error) {
	var list v1alpha1.KapeHandlerList
	if err := r.client.List(ctx, &list, client.MatchingLabels{
		"kape.io/tool-ref-" + toolName: "true",
	}); err != nil {
		return nil, fmt.Errorf("listing handlers by tool ref %q: %w", toolName, err)
	}
	return list.Items, nil
}
