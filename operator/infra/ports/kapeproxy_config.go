package ports

import (
	"context"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// KapeproxyConfigPort manages the kapeproxy-config-{handler-name} ConfigMap.
type KapeproxyConfigPort interface {
	// Ensure creates or updates the kapeproxy config ConfigMap for the given handler.
	// tools must contain only mcp-type KapeTools; memory/event-publish tools are excluded by the caller.
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, tools []v1alpha1.KapeTool) error
}
