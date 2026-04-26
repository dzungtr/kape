package k8s_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
)

func TestToolRepository_Get_Found(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "grafana-mcp", Namespace: "kape-system"},
		Spec:       v1alpha1.KapeToolSpec{Type: "mcp"},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(tool).WithStatusSubresource(tool).Build()
	repo := k8sadapters.NewToolRepository(c)

	got, err := repo.Get(context.Background(), types.NamespacedName{Name: "grafana-mcp", Namespace: "kape-system"})

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "grafana-mcp", got.Name)
}

func TestToolRepository_Get_NotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	repo := k8sadapters.NewToolRepository(c)

	got, err := repo.Get(context.Background(), types.NamespacedName{Name: "missing", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestToolRepository_UpdateStatus(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "mem-tool", Namespace: "kape-system"},
		Spec:       v1alpha1.KapeToolSpec{Type: "memory"},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(tool).WithStatusSubresource(tool).Build()
	repo := k8sadapters.NewToolRepository(c)

	tool.Status.QdrantEndpoint = "http://kape-memory-mem-tool.kape-system:6333"
	err := repo.UpdateStatus(context.Background(), tool)

	require.NoError(t, err)
	got, _ := repo.Get(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})
	assert.Equal(t, "http://kape-memory-mem-tool.kape-system:6333", got.Status.QdrantEndpoint)
}

func TestToolRepository_ListHandlersByToolRef(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-handler",
			Namespace: "kape-system",
			Labels:    map[string]string{"kape.io/tool-ref-grafana-mcp": "true"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(handler).Build()
	repo := k8sadapters.NewToolRepository(c)

	handlers, err := repo.ListHandlersByToolRef(context.Background(), "grafana-mcp")

	require.NoError(t, err)
	require.Len(t, handlers, 1)
	assert.Equal(t, "my-handler", handlers[0].Name)
}
