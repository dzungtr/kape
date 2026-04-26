package k8s_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
)

func TestStatefulSetAdapter_EnsureQdrant_Creates(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "karpenter-memory", Namespace: "kape-system", UID: "uid-1"},
		Spec:       v1alpha1.KapeToolSpec{Type: "memory", Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"}},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(tool).Build()
	adapter := k8sadapters.NewStatefulSetAdapter(c)
	cfg := domainconfig.KapeConfig{QdrantVersion: "v1.14.0", QdrantStorageClass: "standard"}

	err := adapter.EnsureQdrant(context.Background(), tool, cfg)

	require.NoError(t, err)

	// StatefulSet created
	var sts appsv1.StatefulSet
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-memory-karpenter-memory", Namespace: "kape-system"}, &sts)
	require.NoError(t, err)
	assert.Equal(t, "qdrant/qdrant:v1.14.0", sts.Spec.Template.Spec.Containers[0].Image)

	// Headless Service created
	var svc corev1.Service
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-memory-karpenter-memory", Namespace: "kape-system"}, &svc)
	require.NoError(t, err)
	assert.Equal(t, "None", svc.Spec.ClusterIP)
}

func TestStatefulSetAdapter_GetQdrantReadyReplicas_NotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewStatefulSetAdapter(c)

	ready, found, err := adapter.GetQdrantReadyReplicas(context.Background(),
		types.NamespacedName{Name: "kape-memory-missing", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, int32(0), ready)
}
