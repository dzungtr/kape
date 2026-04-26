package k8s_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

func TestScaledObjectAdapter_StreamName_DefaultAndOverride(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
	}
	gvk := schema.GroupVersionKind{Group: "keda.sh", Version: "v1alpha1", Kind: "ScaledObject"}
	soKey := types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}

	t.Run("default stream name applied when cfg unset", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
		adapter := k8sadapters.NewScaledObjectAdapter(c)
		require.NoError(t, adapter.Ensure(context.Background(), handler, "consumer-x", domainconfig.KapeConfig{}))

		got := &unstructured.Unstructured{}
		got.SetGroupVersionKind(gvk)
		require.NoError(t, c.Get(context.Background(), soKey, got))
		triggers, _, _ := unstructured.NestedSlice(got.Object, "spec", "triggers")
		require.Len(t, triggers, 1)
		stream, _, _ := unstructured.NestedString(triggers[0].(map[string]interface{}), "metadata", "streamName")
		assert.Equal(t, "kape-events", stream)
	})

	t.Run("override stream name from cfg propagates to trigger metadata", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
		adapter := k8sadapters.NewScaledObjectAdapter(c)
		cfg := domainconfig.KapeConfig{NATSStreamName: "custom-stream"}
		require.NoError(t, adapter.Ensure(context.Background(), handler, "consumer-x", cfg))

		got := &unstructured.Unstructured{}
		got.SetGroupVersionKind(gvk)
		require.NoError(t, c.Get(context.Background(), soKey, got))
		triggers, _, _ := unstructured.NestedSlice(got.Object, "spec", "triggers")
		require.Len(t, triggers, 1)
		stream, _, _ := unstructured.NestedString(triggers[0].(map[string]interface{}), "metadata", "streamName")
		assert.Equal(t, "custom-stream", stream)
	})
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
