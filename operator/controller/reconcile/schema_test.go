package reconcile_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

func newSchemaScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	return s
}

func validSchema() *v1alpha1.KapeSchema {
	return &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "my-schema", Namespace: "kape-system"},
		Spec: v1alpha1.KapeSchemaSpec{
			Version: "v1",
			JSONSchema: v1alpha1.JSONSchemaObject{
				Type:     "object",
				Required: []string{"decision"},
				Properties: map[string]apiextensionsv1.JSON{
					"decision": {Raw: []byte(`{"type":"string"}`)},
				},
			},
		},
	}
}

func TestSchemaReconciler_ValidSchema_SetsReadyAndHash(t *testing.T) {
	schema := validSchema()
	c := fake.NewClientBuilder().WithScheme(newSchemaScheme()).WithObjects(schema).WithStatusSubresource(schema).Build()
	r := reconcile.NewSchemaReconciler(k8sadapters.NewSchemaRepository(c))

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewSchemaRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionTrue, readyCond.Status)
	assert.NotEmpty(t, got.Status.SchemaHash)
}

func TestSchemaReconciler_InvalidSchema_SetsNotReady(t *testing.T) {
	schema := &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-schema", Namespace: "kape-system"},
		Spec: v1alpha1.KapeSchemaSpec{
			Version: "v1",
			JSONSchema: v1alpha1.JSONSchemaObject{
				Type:     "object",
				Required: []string{"missing-field"}, // not in properties
				Properties: map[string]apiextensionsv1.JSON{
					"decision": {Raw: []byte(`{"type":"string"}`)},
				},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(newSchemaScheme()).WithObjects(schema).WithStatusSubresource(schema).Build()
	r := reconcile.NewSchemaReconciler(k8sadapters.NewSchemaRepository(c))

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "bad-schema", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, false, result.Requeue) // terminal — no requeue
	got, _ := k8sadapters.NewSchemaRepository(c).Get(context.Background(), types.NamespacedName{Name: "bad-schema", Namespace: "kape-system"})
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionFalse, readyCond.Status)
	assert.Equal(t, "InvalidSchema", readyCond.Reason)
}

func TestSchemaReconciler_DeletionBlockedWhenHandlerReferences(t *testing.T) {
	now := metav1.Now()
	schema := validSchema()
	schema.DeletionTimestamp = &now
	schema.Finalizers = []string{"kape.io/schema-protection"}

	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-handler",
			Namespace: "kape-system",
			Labels:    map[string]string{"kape.io/schema-ref": "my-schema"},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newSchemaScheme()).WithObjects(schema, handler).WithStatusSubresource(schema).Build()
	r := reconcile.NewSchemaReconciler(k8sadapters.NewSchemaRepository(c))

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})

	require.NoError(t, err)
	// Finalizer must still be present (deletion blocked)
	got, _ := k8sadapters.NewSchemaRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Contains(t, got.Finalizers, "kape.io/schema-protection")
}

func TestSchemaReconciler_FinalizerAddedOnCreate(t *testing.T) {
	schema := validSchema()
	c := fake.NewClientBuilder().WithScheme(newSchemaScheme()).WithObjects(schema).WithStatusSubresource(schema).Build()
	r := reconcile.NewSchemaReconciler(k8sadapters.NewSchemaRepository(c))

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewSchemaRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})
	assert.Contains(t, got.Finalizers, "kape.io/schema-protection")
}
