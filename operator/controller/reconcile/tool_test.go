package reconcile_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

func newToolScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func newToolFakeRecorder() *record.FakeRecorder {
	return record.NewFakeRecorder(10)
}

// fakeQdrantClusterPort implements ports.QdrantClusterPort for unit tests.
type fakeQdrantClusterPort struct {
	crdInstalled  bool
	ready         bool
	connectionURL string
	found         bool
}

func (f *fakeQdrantClusterPort) IsCRDInstalled(_ context.Context) (bool, error) {
	return f.crdInstalled, nil
}

func (f *fakeQdrantClusterPort) EnsureQdrantCluster(_ context.Context, _ *v1alpha1.KapeTool) error {
	return nil
}

func (f *fakeQdrantClusterPort) GetQdrantClusterStatus(_ context.Context, _ types.NamespacedName) (bool, string, bool, error) {
	return f.ready, f.connectionURL, f.found, nil
}

func TestToolReconciler_MemoryType_CRDNotInstalled_SetsOperatorNotInstalled(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "mem-tool", Namespace: "kape-system", UID: "uid-1"},
		Spec: v1alpha1.KapeToolSpec{
			Type:   "memory",
			Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	qdrantPort := &fakeQdrantClusterPort{crdInstalled: false}
	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), qdrantPort, newToolFakeRecorder())
	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})
	require.NotNil(t, got)
	memReady := findCondition(got.Status.Conditions, "MemoryReady")
	require.NotNil(t, memReady)
	assert.Equal(t, metav1.ConditionFalse, memReady.Status)
	assert.Equal(t, "OperatorNotInstalled", memReady.Reason)
}

func TestToolReconciler_MemoryType_ClusterNotReady_RequeuesAfter15s(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "mem-tool", Namespace: "kape-system", UID: "uid-1"},
		Spec: v1alpha1.KapeToolSpec{
			Type:   "memory",
			Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	qdrantPort := &fakeQdrantClusterPort{crdInstalled: true, ready: false, found: true}
	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), qdrantPort, newToolFakeRecorder())
	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(15), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})
	require.NotNil(t, got)
	memReady := findCondition(got.Status.Conditions, "MemoryReady")
	require.NotNil(t, memReady)
	assert.Equal(t, metav1.ConditionFalse, memReady.Status)
	assert.Equal(t, "QdrantClusterNotReady", memReady.Reason)
}

func TestToolReconciler_MemoryType_ClusterReady_SetsReadyAndEndpoint(t *testing.T) {
	// Mock Qdrant server returns 200 for PUT /collections (EnsureCollection)
	qdrantSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer qdrantSrv.Close()

	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "mem-tool", Namespace: "kape-system", UID: "uid-1"},
		Spec: v1alpha1.KapeToolSpec{
			Type:   "memory",
			Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	qdrantPort := &fakeQdrantClusterPort{
		crdInstalled:  true,
		ready:         true,
		connectionURL: qdrantSrv.URL,
		found:         true,
	}
	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), qdrantPort, newToolFakeRecorder())
	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Equal(t, qdrantSrv.URL, got.Status.QdrantEndpoint)
	memReady := findCondition(got.Status.Conditions, "MemoryReady")
	require.NotNil(t, memReady)
	assert.Equal(t, metav1.ConditionTrue, memReady.Status)
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionTrue, readyCond.Status)
}

func TestToolReconciler_MemoryType_FinalizerAddedOnCreate(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "mem-tool", Namespace: "kape-system", UID: "uid-1"},
		Spec: v1alpha1.KapeToolSpec{
			Type:   "memory",
			Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{crdInstalled: false}, newToolFakeRecorder())
	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Contains(t, got.Finalizers, "kape.io/tool-protection")
}

func TestToolReconciler_MemoryType_UpgradePath_FinalizerAddedRetroactively(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "old-tool",
			Namespace:  "kape-system",
			UID:        "uid-old",
			Finalizers: []string{},
		},
		Spec: v1alpha1.KapeToolSpec{
			Type:   "memory",
			Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{crdInstalled: false}, newToolFakeRecorder())
	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "old-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "old-tool", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Contains(t, got.Finalizers, "kape.io/tool-protection")
}

func TestToolReconciler_MemoryType_DeletionBlockedWhenHandlerReferences(t *testing.T) {
	now := metav1.Now()
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "mem-tool",
			Namespace:         "kape-system",
			DeletionTimestamp: &now,
			Finalizers:        []string{"kape.io/tool-protection"},
		},
		Spec: v1alpha1.KapeToolSpec{
			Type:   "memory",
			Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"},
		},
	}
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-handler",
			Namespace: "kape-system",
			Labels:    map[string]string{"kape.io/tool-ref-mem-tool": "true"},
		},
	}

	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool, handler).WithStatusSubresource(tool).Build()
	rec := newToolFakeRecorder()
	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{}, rec)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Contains(t, got.Finalizers, "kape.io/tool-protection")

	events := drainToolEvents(rec)
	found := false
	for _, e := range events {
		if containsStr(e, "DeletionBlocked") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected a Warning DeletionBlocked event, got: %v", events)
}

func TestToolReconciler_MemoryType_DeletionUnblocksWhenNoHandlers(t *testing.T) {
	now := metav1.Now()
	qdrantSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer qdrantSrv.Close()

	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "mem-tool",
			Namespace:         "kape-system",
			DeletionTimestamp: &now,
			Finalizers:        []string{"kape.io/tool-protection"},
		},
		Spec: v1alpha1.KapeToolSpec{
			Type:   "memory",
			Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"},
		},
		Status: v1alpha1.KapeToolStatus{
			QdrantEndpoint: qdrantSrv.URL,
		},
	}

	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()
	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{}, newToolFakeRecorder())

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})
	if got != nil {
		assert.NotContains(t, got.Finalizers, "kape.io/tool-protection")
	}
}

func TestToolReconciler_MemoryType_DeleteCollection_Idempotent404(t *testing.T) {
	now := metav1.Now()
	qdrantSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer qdrantSrv.Close()

	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "mem-tool",
			Namespace:         "kape-system",
			DeletionTimestamp: &now,
			Finalizers:        []string{"kape.io/tool-protection"},
		},
		Spec: v1alpha1.KapeToolSpec{
			Type:   "memory",
			Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"},
		},
		Status: v1alpha1.KapeToolStatus{
			QdrantEndpoint: qdrantSrv.URL,
		},
	}

	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()
	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{}, newToolFakeRecorder())

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "mem-tool", Namespace: "kape-system"})
	if got != nil {
		assert.NotContains(t, got.Finalizers, "kape.io/tool-protection")
	}
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

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{}, newToolFakeRecorder())
	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mcp-tool", Namespace: "kape-system"})

	require.NoError(t, err)
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

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{}, newToolFakeRecorder())
	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mcp-down", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "mcp-down", Namespace: "kape-system"})
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionFalse, readyCond.Status)
	assert.Equal(t, "MCPEndpointUnreachable", readyCond.Reason)
}

func TestToolReconciler_MCPType_SkipProbe_SetsReadyWithoutHTTPCall(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "mcp-skip", Namespace: "kape-system"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "mcp",
			MCP: &v1alpha1.MCPSpec{
				Upstream:  v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://127.0.0.1:19999"},
				SkipProbe: true,
			},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{}, newToolFakeRecorder())
	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "mcp-skip", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "mcp-skip", Namespace: "kape-system"})
	require.NotNil(t, got)
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionTrue, readyCond.Status)
	assert.Equal(t, "ProbeSkipped", readyCond.Reason)
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

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{}, newToolFakeRecorder())
	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "ep-tool", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "ep-tool", Namespace: "kape-system"})
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionTrue, readyCond.Status)
}

func TestToolReconciler_ExternalMemory_SetsReadyWithExternalURL(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "ext-mem", Namespace: "kape-system"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "memory",
			Memory: &v1alpha1.MemorySpec{
				Backend:        "qdrant",
				DistanceMetric: "cosine",
				External: &v1alpha1.ExternalMemorySpec{
					URL: "http://my-qdrant.example.com:6333",
				},
			},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{}, newToolFakeRecorder())
	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "ext-mem", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "ext-mem", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Equal(t, "http://my-qdrant.example.com:6333", got.Status.QdrantEndpoint)
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionTrue, readyCond.Status)
	assert.Equal(t, "ExternalDatabase", readyCond.Reason)
}

func TestToolReconciler_ExternalMemory_WithSecretRef_SetsReady(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "ext-mem-secret", Namespace: "kape-system"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "memory",
			Memory: &v1alpha1.MemorySpec{
				Backend:        "qdrant",
				DistanceMetric: "cosine",
				External: &v1alpha1.ExternalMemorySpec{
					URL: "http://secure-qdrant.example.com:6333",
					SecretRef: &corev1.LocalObjectReference{
						Name: "my-qdrant-api-key",
					},
				},
			},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{}, newToolFakeRecorder())
	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "ext-mem-secret", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "ext-mem-secret", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Equal(t, "http://secure-qdrant.example.com:6333", got.Status.QdrantEndpoint)
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionTrue, readyCond.Status)
	assert.Equal(t, "ExternalDatabase", readyCond.Reason)
}

func TestToolReconciler_ExternalMemory_MalformedURL_ReportsError(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "ext-mem-bad-url", Namespace: "kape-system"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "memory",
			Memory: &v1alpha1.MemorySpec{
				Backend:        "qdrant",
				DistanceMetric: "cosine",
				External: &v1alpha1.ExternalMemorySpec{
					URL: "my-qdrant",
				},
			},
		},
	}
	s := newToolScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).WithStatusSubresource(tool).Build()

	r := reconcile.NewToolReconciler(k8sadapters.NewToolRepository(c), &fakeQdrantClusterPort{}, newToolFakeRecorder())
	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "ext-mem-bad-url", Namespace: "kape-system"})

	require.Error(t, err, "reconciler must reject external URL without http(s):// scheme")
	assert.Contains(t, err.Error(), "must start with http:// or https://")

	got, _ := k8sadapters.NewToolRepository(c).Get(context.Background(), types.NamespacedName{Name: "ext-mem-bad-url", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Empty(t, got.Status.QdrantEndpoint, "QdrantEndpoint must not be set for a malformed external URL")
	readyCond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionFalse, readyCond.Status)
	assert.Equal(t, "InvalidExternalURL", readyCond.Reason)
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

func drainToolEvents(rec *record.FakeRecorder) []string {
	var events []string
	for {
		select {
		case e := <-rec.Events:
			events = append(events, e)
		default:
			return events
		}
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

type fakeConfigLoader struct{}

func (f *fakeConfigLoader) Load(_ context.Context) (domainconfig.KapeConfig, error) {
	return domainconfig.KapeConfig{}, nil
}
