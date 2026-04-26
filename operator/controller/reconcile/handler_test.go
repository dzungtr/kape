package reconcile_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	tomlrenderer "github.com/kape-io/kape/operator/infra/toml"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

func newHandlerScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func readySchema(name, ns string) *v1alpha1.KapeSchema {
	return &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
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
		Status: v1alpha1.KapeSchemaStatus{
			Conditions: []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionTrue, Reason: "Valid",
			}},
		},
	}
}

func readyTool(name, ns, toolType string) *v1alpha1.KapeTool {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       v1alpha1.KapeToolSpec{Type: toolType},
		Status: v1alpha1.KapeToolStatus{
			Conditions: []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready",
			}},
		},
	}
	if toolType == "mcp" {
		tool.Spec.MCP = &v1alpha1.MCPSpec{
			Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://mcp:8080"},
		}
	}
	if toolType == "memory" {
		tool.Spec.Memory = &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"}
		tool.Status.QdrantEndpoint = "http://kape-memory-" + name + ".kape-system:6333"
	}
	return tool
}

func baseKapeHandler(name, ns, schemaRef string, toolRefs []string) *v1alpha1.KapeHandler {
	refs := make([]v1alpha1.ToolRef, len(toolRefs))
	for i, r := range toolRefs {
		refs[i] = v1alpha1.ToolRef{Ref: r}
	}
	return &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: "uid-h"},
		Spec: v1alpha1.KapeHandlerSpec{
			Trigger:   v1alpha1.TriggerSpec{Source: "alertmanager", Type: "kape.events.test"},
			LLM:       v1alpha1.LLMSpec{Provider: "anthropic", Model: "claude-3", SystemPrompt: "test"},
			SchemaRef: schemaRef,
			Tools:     refs,
			Actions:   []v1alpha1.ActionSpec{},
		},
	}
}

func TestHandlerReconciler_SchemaNotReady_RequeuePending(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	schema.Status.Conditions[0].Status = metav1.ConditionFalse // not ready
	handler := baseKapeHandler("my-handler", "kape-system", "my-schema", nil)

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema).
		WithStatusSubresource(handler, schema).
		Build()

	r := reconcile.NewHandlerReconciler(
		k8sadapters.NewHandlerRepository(c),
		k8sadapters.NewSchemaRepository(c),
		k8sadapters.NewToolRepository(c),
		k8sadapters.NewConfigMapAdapter(c),
		k8sadapters.NewServiceAccountAdapter(c),
		k8sadapters.NewDeploymentAdapter(c),
		k8sadapters.NewScaledObjectAdapter(c),
		tomlrenderer.NewRenderer(),
		&fakeConfigLoader{},
	)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})
	depsCond := findCondition(got.Status.Conditions, "DependenciesReady")
	require.NotNil(t, depsCond)
	assert.Equal(t, metav1.ConditionFalse, depsCond.Status)
}

func TestHandlerReconciler_ToolNotReady_RequeuePending(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("grafana-mcp", "kape-system", "mcp")
	tool.Status.Conditions[0].Status = metav1.ConditionFalse // not ready
	handler := baseKapeHandler("my-handler", "kape-system", "my-schema", []string{"grafana-mcp"})

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool).
		WithStatusSubresource(handler, schema, tool).
		Build()

	r := reconcile.NewHandlerReconciler(
		k8sadapters.NewHandlerRepository(c),
		k8sadapters.NewSchemaRepository(c),
		k8sadapters.NewToolRepository(c),
		k8sadapters.NewConfigMapAdapter(c),
		k8sadapters.NewServiceAccountAdapter(c),
		k8sadapters.NewDeploymentAdapter(c),
		k8sadapters.NewScaledObjectAdapter(c),
		tomlrenderer.NewRenderer(),
		&fakeConfigLoader{},
	)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))
}

func TestHandlerReconciler_InvalidScaling_TerminalNoRequeue(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	handler := baseKapeHandler("my-handler", "kape-system", "my-schema", nil)
	handler.Spec.Scaling = &v1alpha1.ScalingSpec{ScaleToZero: true, MinReplicas: 1} // invalid

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema).
		WithStatusSubresource(handler, schema).
		Build()

	r := reconcile.NewHandlerReconciler(
		k8sadapters.NewHandlerRepository(c),
		k8sadapters.NewSchemaRepository(c),
		k8sadapters.NewToolRepository(c),
		k8sadapters.NewConfigMapAdapter(c),
		k8sadapters.NewServiceAccountAdapter(c),
		k8sadapters.NewDeploymentAdapter(c),
		k8sadapters.NewScaledObjectAdapter(c),
		tomlrenderer.NewRenderer(),
		&fakeConfigLoader{},
	)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, false, result.Requeue)
	assert.Equal(t, int64(0), int64(result.RequeueAfter))
}

func TestHandlerReconciler_AllDepsReady_CreatesResources(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("grafana-mcp", "kape-system", "mcp")
	handler := baseKapeHandler("my-handler", "kape-system", "my-schema", []string{"grafana-mcp"})

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool).
		WithStatusSubresource(handler, schema, tool).
		Build()

	r := reconcile.NewHandlerReconciler(
		k8sadapters.NewHandlerRepository(c),
		k8sadapters.NewSchemaRepository(c),
		k8sadapters.NewToolRepository(c),
		k8sadapters.NewConfigMapAdapter(c),
		k8sadapters.NewServiceAccountAdapter(c),
		k8sadapters.NewDeploymentAdapter(c),
		k8sadapters.NewScaledObjectAdapter(c),
		tomlrenderer.NewRenderer(),
		&fakeConfigLoader{},
	)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-handler", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(60), int64(result.RequeueAfter.Seconds()))

	// ConfigMap created
	var cm corev1.ConfigMap
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-my-handler", Namespace: "kape-system"}, &cm)
	require.NoError(t, err)

	// Deployment created
	var dep appsv1.Deployment
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-my-handler", Namespace: "kape-system"}, &dep)
	require.NoError(t, err)
	assert.Len(t, dep.Spec.Template.Spec.Containers, 2) // handler + sidecar

	// ServiceAccount created
	var sa corev1.ServiceAccount
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-my-handler", Namespace: "kape-system"}, &sa)
	require.NoError(t, err)
}
