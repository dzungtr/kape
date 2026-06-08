//go:build envtest

package integration_test

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// noopScaledObjectAdapter is used in integration tests because KEDA CRDs are
// not installed in the envtest cluster. Without this, the handler reconciler
// would fail on every reconcile attempting to create a ScaledObject.
type noopScaledObjectAdapter struct{}

func (n *noopScaledObjectAdapter) Ensure(_ context.Context, _ *v1alpha1.KapeHandler, _ string, _ domainconfig.KapeConfig) error {
	return nil
}

func (n *noopScaledObjectAdapter) GetConsumerName(_ context.Context, _ types.NamespacedName) (string, bool, error) {
	return "", false, nil
}

func (n *noopScaledObjectAdapter) Delete(_ context.Context, _ types.NamespacedName) error {
	return nil
}
