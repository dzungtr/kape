// Package ports defines outbound interfaces for the KAPE operator's handler reconciler.
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
	// Get fetches the KapeHandler by namespaced name.
	Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeHandler, error)

	// UpdateStatus persists status sub-resource changes.
	UpdateStatus(ctx context.Context, handler *v1alpha1.KapeHandler) error

	// SyncLabels merges the provided labels onto the KapeHandler, overwriting existing values.
	SyncLabels(ctx context.Context, handler *v1alpha1.KapeHandler, labels map[string]string) error
}

// ConfigMapPort manages the handler settings.toml ConfigMap.
type ConfigMapPort interface {
	// Ensure creates or updates the settings.toml ConfigMap for the given handler.
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, tomlContent string) error
}

// ServiceAccountPort manages the per-handler ServiceAccount.
type ServiceAccountPort interface {
	// Ensure creates the handler ServiceAccount if absent. Idempotent.
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler) error
}

// DeploymentPort manages the handler Deployment.
type DeploymentPort interface {
	// Ensure creates or patches the handler Deployment.
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, cfg domainconfig.KapeConfig, rolloutHash string) error

	// GetStatus reads the current Deployment status. found is false when the Deployment does not exist.
	GetStatus(ctx context.Context, key types.NamespacedName) (status *appsv1.DeploymentStatus, found bool, err error)
}

// KapeConfigLoader reads the kape-config ConfigMap and returns operator platform config.
type KapeConfigLoader interface {
	Load(ctx context.Context) (domainconfig.KapeConfig, error)
}

// TOMLRenderer produces a settings.toml string from a KapeHandler spec and platform config.
type TOMLRenderer interface {
	Render(handler *v1alpha1.KapeHandler, cfg domainconfig.KapeConfig) (string, error)
}
