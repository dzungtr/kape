package ports

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/types"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// ErrNotOwned reports an object the operator did not render: the apply layer
// refuses to patch or delete it (M5 invariant — never adopt).
var ErrNotOwned = errors.New("object is not owned by this KapeAgentServer")

// AgentServerRepository reads and writes KapeAgentServer resources.
type AgentServerRepository interface {
	// Get fetches the KapeAgentServer by namespaced name. Returns nil, nil when
	// not found.
	Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeAgentServer, error)
	// UpdateStatus persists the status sub-resource.
	UpdateStatus(ctx context.Context, server *v1alpha1.KapeAgentServer) error
}
