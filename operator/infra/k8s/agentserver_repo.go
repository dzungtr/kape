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

// AgentServerRepository implements ports.AgentServerRepository.
type AgentServerRepository struct {
	client client.Client
}

// NewAgentServerRepository creates a new AgentServerRepository.
func NewAgentServerRepository(c client.Client) *AgentServerRepository {
	return &AgentServerRepository{client: c}
}

// Get fetches the KapeAgentServer by namespaced name. Returns nil, nil when not found.
func (r *AgentServerRepository) Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeAgentServer, error) {
	var server v1alpha1.KapeAgentServer
	if err := r.client.Get(ctx, key, &server); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return &server, nil
}

// UpdateStatus persists the server's status sub-resource using RetryOnConflict.
// Returns nil on NotFound (the CR was deleted between reconcile steps).
func (r *AgentServerRepository) UpdateStatus(ctx context.Context, server *v1alpha1.KapeAgentServer) error {
	key := types.NamespacedName{Name: server.Name, Namespace: server.Namespace}
	desiredStatus := server.Status

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest v1alpha1.KapeAgentServer
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
		return fmt.Errorf("updating KapeAgentServer %s/%s status: %w", server.Namespace, server.Name, err)
	}
	return nil
}
