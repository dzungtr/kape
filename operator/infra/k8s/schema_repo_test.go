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

func TestSchemaRepository_Get_Found(t *testing.T) {
	schema := &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "my-schema", Namespace: "kape-system"},
		Spec:       v1alpha1.KapeSchemaSpec{Version: "v1"},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(schema).WithStatusSubresource(schema).Build()
	repo := k8sadapters.NewSchemaRepository(c)

	got, err := repo.Get(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "my-schema", got.Name)
}

func TestSchemaRepository_Get_NotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	repo := k8sadapters.NewSchemaRepository(c)

	got, err := repo.Get(context.Background(), types.NamespacedName{Name: "missing", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSchemaRepository_UpdateStatus(t *testing.T) {
	schema := &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "my-schema", Namespace: "kape-system"},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(schema).WithStatusSubresource(schema).Build()
	repo := k8sadapters.NewSchemaRepository(c)

	schema.Status.SchemaHash = "abc123"
	err := repo.UpdateStatus(context.Background(), schema)

	require.NoError(t, err)
	got, _ := repo.Get(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})
	assert.Equal(t, "abc123", got.Status.SchemaHash)
}

func TestSchemaRepository_AddRemoveFinalizer(t *testing.T) {
	schema := &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "my-schema", Namespace: "kape-system"},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(schema).Build()
	repo := k8sadapters.NewSchemaRepository(c)

	err := repo.AddFinalizer(context.Background(), schema, "kape.io/schema-protection")
	require.NoError(t, err)

	got, _ := repo.Get(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})
	assert.Contains(t, got.Finalizers, "kape.io/schema-protection")

	err = repo.RemoveFinalizer(context.Background(), got, "kape.io/schema-protection")
	require.NoError(t, err)

	got2, _ := repo.Get(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})
	assert.NotContains(t, got2.Finalizers, "kape.io/schema-protection")
}

func TestSchemaRepository_ListHandlersBySchemaRef(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-handler",
			Namespace: "kape-system",
			Labels:    map[string]string{"kape.io/schema-ref": "my-schema"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(handler).Build()
	repo := k8sadapters.NewSchemaRepository(c)

	handlers, err := repo.ListHandlersBySchemaRef(context.Background(), "my-schema")

	require.NoError(t, err)
	require.Len(t, handlers, 1)
	assert.Equal(t, "my-handler", handlers[0].Name)
}
