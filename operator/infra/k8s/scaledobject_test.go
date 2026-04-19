package k8s_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
)

func TestScaledObjectAdapter_EnsureAndGetConsumerName(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "my-handler", Namespace: "kape-system", UID: "uid-h"},
		Spec: v1alpha1.KapeHandlerSpec{
			Scaling: &v1alpha1.ScalingSpec{MinReplicas: 1, MaxReplicas: 5, NatsLagThreshold: 5, ScaleDownStabilizationSeconds: 60},
		},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewScaledObjectAdapter(c)
	cfg := domainconfig.KapeConfig{NATSMonitoringEndpoint: "http://nats.kape-system:8222"}

	err := adapter.Ensure(context.Background(), handler, "kape-events-test", cfg)
	require.NoError(t, err)

	name, found, err := adapter.GetConsumerName(context.Background(),
		types.NamespacedName{Name: "kape-handler-my-handler", Namespace: "kape-system"})
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "kape-events-test", name)
}

func TestScaledObjectAdapter_GetConsumerName_NotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewScaledObjectAdapter(c)

	_, found, err := adapter.GetConsumerName(context.Background(),
		types.NamespacedName{Name: "missing", Namespace: "kape-system"})
	require.NoError(t, err)
	assert.False(t, found)
}

func TestScaledObjectAdapter_Delete(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "my-handler", Namespace: "kape-system", UID: "uid-h"},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewScaledObjectAdapter(c)
	cfg := domainconfig.KapeConfig{}

	_ = adapter.Ensure(context.Background(), handler, "consumer-1", cfg)

	err := adapter.Delete(context.Background(), types.NamespacedName{Name: "kape-handler-my-handler", Namespace: "kape-system"})
	require.NoError(t, err)

	_, found, _ := adapter.GetConsumerName(context.Background(),
		types.NamespacedName{Name: "kape-handler-my-handler", Namespace: "kape-system"})
	assert.False(t, found)
}
