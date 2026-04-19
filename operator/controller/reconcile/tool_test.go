package reconcile_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

func newToolScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func TestToolReconciler_MemoryType_CreatesQdrant(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "mem-tool", Namespace: "kape-system", UID: "uid-1"},
		Spec: v1alpha1.KapeToolSpec{
			Type:   "memory",
			Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	toolRepo := k8sadapters.NewToolRepository(c)
	statefulSetAdapter := k8sadapters.NewStatefulSetAdapter(c)
	cfgLoader := &fakeConfigLoader{}

	r := reconcile.NewToolReconciler(toolRepo, statefulSetAdapter, cfgLoader)
	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	// StatefulSet not ready yet → requeue after 15s
	assert.Equal(t, int64(15), int64(result.RequeueAfter.Seconds()))

	// StatefulSet was created
	var sts appsv1.StatefulSet
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-memory-mem-tool", Namespace: "kape-system"}, &sts)
	require.NoError(t, err)
}

func TestToolReconciler_MCPType_EndpointReachable_SetsReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "mcp-tool", Namespace: "kape-system"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "mcp",
			MCP:  &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: srv.URL}},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), k8sadapters.NewStatefulSetAdapter(c), &fakeConfigLoader{})
	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mcp-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	// Periodic health refresh
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "mcp-tool", Namespace: "kape-system"})
	require.NotNil(t, got)
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionTrue, readyCond.Status)
}

func TestToolReconciler_MCPType_EndpointUnreachable_SetsNotReady(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "mcp-down", Namespace: "kape-system"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "mcp",
			MCP:  &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://127.0.0.1:19999"}},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), k8sadapters.NewStatefulSetAdapter(c), &fakeConfigLoader{})
	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mcp-down", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "mcp-down", Namespace: "kape-system"})
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionFalse, readyCond.Status)
	assert.Equal(t, "MCPEndpointUnreachable", readyCond.Reason)
}

func TestToolReconciler_EventPublish_ValidType_SetsReady(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "ep-tool", Namespace: "kape-system"},
		Spec: v1alpha1.KapeToolSpec{
			Type:         "event-publish",
			EventPublish: &v1alpha1.EventPublishSpec{Type: "kape.events.gitops.pr-requested"},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), k8sadapters.NewStatefulSetAdapter(c), &fakeConfigLoader{})
	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "ep-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "ep-tool", Namespace: "kape-system"})
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionTrue, readyCond.Status)
}

// ─── helpers ────────────────────────────────────────────────────────────────

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

type fakeConfigLoader struct{}

func (f *fakeConfigLoader) Load(_ context.Context) (domainconfig.KapeConfig, error) {
	return domainconfig.KapeConfig{}, nil
}

// Ensure unused imports compile (ctrl is used in test file via result type)
var _ ctrl.Result
