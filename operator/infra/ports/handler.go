package ports

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// HandlerRepository reads and writes KapeHandler resources.
type HandlerRepository interface {
	Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeHandler, error)
	UpdateStatus(ctx context.Context, handler *v1alpha1.KapeHandler) error
	SyncLabels(ctx context.Context, handler *v1alpha1.KapeHandler, labels map[string]string) error
}

// ConfigMapPort manages the handler settings.toml ConfigMap.
type ConfigMapPort interface {
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, tomlContent string) error
}

// ServiceAccountPort manages the per-handler ServiceAccount.
type ServiceAccountPort interface {
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler) error
}

// DeploymentPort manages the handler Deployment.
type DeploymentPort interface {
	// Ensure creates or patches the handler Deployment with sidecar injection for mcp-type tools.
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, cfg domainconfig.KapeConfig, rolloutHash string, tools []v1alpha1.KapeTool) error
	// GetStatus reads the current Deployment status. found is false when the Deployment does not exist.
	GetStatus(ctx context.Context, key types.NamespacedName) (status *appsv1.DeploymentStatus, found bool, err error)
}

// KapeConfigLoader reads the kape-config ConfigMap and returns operator platform config.
type KapeConfigLoader interface {
	Load(ctx context.Context) (domainconfig.KapeConfig, error)
}

// TOMLRenderer produces a settings.toml string from a KapeHandler, its resolved schema,
// its resolved tools, and platform config.
type TOMLRenderer interface {
	Render(
		handler *v1alpha1.KapeHandler,
		schema *v1alpha1.KapeSchema,
		tools []v1alpha1.KapeTool,
		cfg domainconfig.KapeConfig,
	) (string, error)
}
