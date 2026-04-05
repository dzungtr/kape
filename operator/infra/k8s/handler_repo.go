// Package k8s provides Kubernetes adapter implementations for the KAPE operator ports.
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

// HandlerRepository implements ports.HandlerRepository using controller-runtime client.
type HandlerRepository struct {
	client client.Client
}

// NewHandlerRepository creates a new HandlerRepository.
func NewHandlerRepository(c client.Client) *HandlerRepository {
	return &HandlerRepository{client: c}
}

// Get fetches the KapeHandler by namespaced name. Returns nil, nil when not found.
func (r *HandlerRepository) Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeHandler, error) {
	var handler v1alpha1.KapeHandler
	if err := r.client.Get(ctx, key, &handler); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return &handler, nil
}

// UpdateStatus persists the handler's status sub-resource using RetryOnConflict.
// Returns nil on NotFound (handler was deleted between reconcile steps).
func (r *HandlerRepository) UpdateStatus(ctx context.Context, handler *v1alpha1.KapeHandler) error {
	key := types.NamespacedName{Name: handler.Name, Namespace: handler.Namespace}
	desiredStatus := handler.Status

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest v1alpha1.KapeHandler
		if err := r.client.Get(ctx, key, &latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		latest.Status = desiredStatus
		return r.client.Status().Update(ctx, &latest)
	})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("updating KapeHandler %s/%s status: %w", handler.Namespace, handler.Name, err)
	}
	return nil
}

// SyncLabels merges labels onto the KapeHandler resource.
// Returns nil on NotFound (handler was deleted between reconcile steps).
func (r *HandlerRepository) SyncLabels(ctx context.Context, handler *v1alpha1.KapeHandler, labels map[string]string) error {
	patch := client.MergeFrom(handler.DeepCopy())
	if handler.Labels == nil {
		handler.Labels = make(map[string]string)
	}
	for k, v := range labels {
		handler.Labels[k] = v
	}
	if err := r.client.Patch(ctx, handler, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("syncing labels on KapeHandler %s/%s: %w", handler.Namespace, handler.Name, err)
	}
	return nil
}
