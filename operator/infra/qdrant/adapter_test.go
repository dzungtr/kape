package qdrant_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	discoveryfake "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/qdrant"
)

var qdrantClusterGVK = schema.GroupVersionKind{
	Group:   "qdrant.io",
	Version: "v1alpha1",
	Kind:    "QdrantCluster",
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	return s
}

func fakeDiscovery(installed bool) *discoveryfake.FakeDiscovery {
	fd := &discoveryfake.FakeDiscovery{Fake: &clienttesting.Fake{}}
	if installed {
		fd.Resources = []*metav1.APIResourceList{
			{
				GroupVersion: "qdrant.io/v1alpha1",
				APIResources: []metav1.APIResource{
					{Name: "qdrantclusters", Kind: "QdrantCluster", Namespaced: true},
				},
			},
		}
	}
	return fd
}

func TestQdrantClusterAdapter_IsCRDInstalled_True(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	adapter := qdrant.NewQdrantClusterAdapter(c, fakeDiscovery(true))

	installed, err := adapter.IsCRDInstalled(context.Background())

	require.NoError(t, err)
	assert.True(t, installed)
}

func TestQdrantClusterAdapter_IsCRDInstalled_False(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	adapter := qdrant.NewQdrantClusterAdapter(c, fakeDiscovery(false))

	installed, err := adapter.IsCRDInstalled(context.Background())

	require.NoError(t, err)
	assert.False(t, installed)
}

func TestQdrantClusterAdapter_GetQdrantClusterStatus_NotFound(t *testing.T) {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	adapter := qdrant.NewQdrantClusterAdapter(c, fakeDiscovery(true))

	ready, url, found, err := adapter.GetQdrantClusterStatus(context.Background(),
		types.NamespacedName{Name: "kape-memory-missing", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.False(t, found)
	assert.False(t, ready)
	assert.Empty(t, url)
}

func TestQdrantClusterAdapter_GetQdrantClusterStatus_Ready(t *testing.T) {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "qdrant.io/v1alpha1",
			"kind":       "QdrantCluster",
			"metadata": map[string]interface{}{
				"name":      "kape-memory-mem-tool",
				"namespace": "kape-system",
			},
			"status": map[string]interface{}{
				"url": "http://kape-memory-mem-tool.kape-system:6333",
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "True",
					},
				},
			},
		},
	}
	obj.SetGroupVersionKind(qdrantClusterGVK)

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(obj).
		Build()
	adapter := qdrant.NewQdrantClusterAdapter(c, fakeDiscovery(true))

	ready, url, found, err := adapter.GetQdrantClusterStatus(context.Background(),
		types.NamespacedName{Name: "kape-memory-mem-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.True(t, found)
	assert.True(t, ready)
	assert.Equal(t, "http://kape-memory-mem-tool.kape-system:6333", url)
}

func TestQdrantClusterAdapter_GetQdrantClusterStatus_NotReady(t *testing.T) {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "qdrant.io/v1alpha1",
			"kind":       "QdrantCluster",
			"metadata": map[string]interface{}{
				"name":      "kape-memory-mem-tool",
				"namespace": "kape-system",
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "False",
					},
				},
			},
		},
	}
	obj.SetGroupVersionKind(qdrantClusterGVK)

	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(obj).
		Build()
	adapter := qdrant.NewQdrantClusterAdapter(c, fakeDiscovery(true))

	ready, url, found, err := adapter.GetQdrantClusterStatus(context.Background(),
		types.NamespacedName{Name: "kape-memory-mem-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.True(t, found)
	assert.False(t, ready)
	assert.Empty(t, url)
}

func TestQdrantClusterAdapter_EnsureQdrantCluster_Creates(t *testing.T) {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)

	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "mem-tool", Namespace: "kape-system", UID: "uid-1"},
		Spec: v1alpha1.KapeToolSpec{
			Type:   "memory",
			Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"},
		},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).Build()
	adapter := qdrant.NewQdrantClusterAdapter(c, fakeDiscovery(true))

	err := adapter.EnsureQdrantCluster(context.Background(), tool)
	require.NoError(t, err)

	var got unstructured.Unstructured
	got.SetGroupVersionKind(qdrantClusterGVK)
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-memory-mem-tool", Namespace: "kape-system"}, &got)
	require.NoError(t, err)
	assert.Equal(t, "kape-memory-mem-tool", got.GetName())

	// Verify owner reference
	refs := got.GetOwnerReferences()
	require.Len(t, refs, 1)
	assert.Equal(t, "KapeTool", refs[0].Kind)
	assert.Equal(t, "mem-tool", refs[0].Name)
}
