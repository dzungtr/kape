# Phase 6 — Plan B: Reconcilers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Prerequisite:** Plan A must be complete. All files from `operator/infra/ports/`, `operator/infra/k8s/`, and `operator/infra/toml/` must be in place before starting.

**Goal:** Implement the three reconcilers (KapeToolReconciler, KapeSchemaReconciler, KapeHandlerReconciler), cross-resource watches, and rewire main.go.

**Architecture:** Each reconciler is a thin controller-runtime wrapper delegating to a domain reconcile struct. The handler reconciler is a full rewrite of the Phase 2 version implementing the 12-step flow from spec 0005.

**Tech Stack:** Go 1.24, controller-runtime v0.19, testify, fake client for unit tests

---

## File Map

| File | Action | Purpose |
|---|---|---|
| `operator/controller/reconcile/tool.go` | Create | KapeTool reconcile logic (3 dispatch paths) |
| `operator/controller/reconcile/tool_test.go` | Create | Tool reconciler tests |
| `operator/controller/reconcile/schema.go` | Create | KapeSchema reconcile logic |
| `operator/controller/reconcile/schema_test.go` | Create | Schema reconciler tests |
| `operator/controller/reconcile/handler.go` | Replace | Full 12-step KapeHandler reconcile (replaces Phase 2) |
| `operator/controller/reconcile/handler_test.go` | Create | Handler reconciler tests |
| `operator/controller/tool.go` | Create | Thin KapeToolReconciler + SetupToolReconciler |
| `operator/controller/schema.go` | Create | Thin KapeSchemaReconciler + SetupSchemaReconciler |
| `operator/controller/handler.go` | Modify | Add secondary watches (KapeTool, KapeSchema → handler) |
| `operator/controller/watches.go` | Create | Map functions for secondary watches |
| `operator/cmd/main.go` | Replace | Wire all three reconcilers |

---

## Task 1: KapeToolReconciler

**Files:**
- Create: `operator/controller/reconcile/tool.go`
- Create: `operator/controller/reconcile/tool_test.go`
- Create: `operator/controller/tool.go`

- [ ] **Step 1: Write the failing tests**

Create `operator/controller/reconcile/tool_test.go`:

```go
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
```

Add the missing import for `domainconfig` to the test file — it needs:
```go
domainconfig "github.com/kape-io/kape/operator/domain/config"
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
cd operator && go test ./controller/reconcile/... -run TestToolReconciler -v 2>&1 | head -20
```

Expected: compile error — `reconcile.NewToolReconciler` undefined.

- [ ] **Step 3: Implement ToolReconciler**

Create `operator/controller/reconcile/tool.go`:

```go
package reconcile

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

// ToolReconciler performs the full reconcile logic for KapeTool.
type ToolReconciler struct {
	tools       ports.ToolRepository
	statefulSet ports.StatefulSetPort
	kapeConfig  ports.KapeConfigLoader
}

// NewToolReconciler creates a ToolReconciler.
func NewToolReconciler(
	tools ports.ToolRepository,
	statefulSet ports.StatefulSetPort,
	kapeConfig ports.KapeConfigLoader,
) *ToolReconciler {
	return &ToolReconciler{tools: tools, statefulSet: statefulSet, kapeConfig: kapeConfig}
}

// Reconcile dispatches on spec.type.
func (r *ToolReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	tool, err := r.tools.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeTool: %w", err)
	}
	if tool == nil {
		return ctrl.Result{}, nil
	}

	switch tool.Spec.Type {
	case "memory":
		return r.reconcileMemory(ctx, tool)
	case "mcp":
		return r.reconcileMCP(ctx, tool)
	case "event-publish":
		return r.reconcileEventPublish(ctx, tool)
	default:
		return ctrl.Result{}, nil
	}
}

func (r *ToolReconciler) reconcileMemory(ctx context.Context, tool *v1alpha1.KapeTool) (ctrl.Result, error) {
	cfg, err := r.kapeConfig.Load(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading kape-config: %w", err)
	}

	if err := r.statefulSet.EnsureQdrant(ctx, tool, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring Qdrant: %w", err)
	}

	stsKey := types.NamespacedName{Name: "kape-memory-" + tool.Name, Namespace: tool.Namespace}
	readyReplicas, found, err := r.statefulSet.GetQdrantReadyReplicas(ctx, stsKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading Qdrant status: %w", err)
	}

	if !found || readyReplicas < 1 {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "QdrantNotReady",
			Message: "StatefulSet has no ready replicas",
		})
		tool.Status.QdrantEndpoint = ""
		if err := r.tools.UpdateStatus(ctx, tool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	endpoint := fmt.Sprintf("http://kape-memory-%s.%s:6333", tool.Name, tool.Namespace)
	tool.Status.QdrantEndpoint = endpoint
	tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Ready",
		Message: "Qdrant StatefulSet ready",
	})
	if err := r.tools.UpdateStatus(ctx, tool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *ToolReconciler) reconcileMCP(ctx context.Context, tool *v1alpha1.KapeTool) (ctrl.Result, error) {
	err := probeMCPEndpoint(tool.Spec.MCP.Upstream.URL)
	if err != nil {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "MCPEndpointUnreachable",
			Message: fmt.Sprintf("Health probe failed: %v", err),
		})
	} else {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionTrue,
			Reason:  "Ready",
			Message: "MCP endpoint reachable",
		})
	}
	if err := r.tools.UpdateStatus(ctx, tool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *ToolReconciler) reconcileEventPublish(ctx context.Context, tool *v1alpha1.KapeTool) (ctrl.Result, error) {
	ep := tool.Spec.EventPublish
	if ep == nil || !strings.HasPrefix(ep.Type, "kape.events.") {
		tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ValidationFailed",
			Message: "spec.eventPublish.type must start with 'kape.events.'",
		})
		if err := r.tools.UpdateStatus(ctx, tool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil // terminal — no requeue
	}

	tool.Status.Conditions = setCondition(tool.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Ready",
		Message: "event-publish type valid",
	})
	if err := r.tools.UpdateStatus(ctx, tool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// probeMCPEndpoint performs an HTTP GET to {url}/health with 5s timeout, 3 attempts.
func probeMCPEndpoint(rawURL string) error {
	healthURL := strings.TrimRight(rawURL, "/") + "/health"
	httpClient := &http.Client{Timeout: 5 * time.Second}
	var lastErr error
	for i := 0; i < 3; i++ {
		resp, err := httpClient.Get(healthURL)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return lastErr
}

// setCondition upserts a condition by type, preserving LastTransitionTime when status is unchanged.
func setCondition(conditions []metav1.Condition, c metav1.Condition) []metav1.Condition {
	c.LastTransitionTime = metav1.Now()
	for i, existing := range conditions {
		if existing.Type == c.Type {
			if existing.Status == c.Status {
				c.LastTransitionTime = existing.LastTransitionTime
			}
			conditions[i] = c
			return conditions
		}
	}
	return append(conditions, c)
}
```

- [ ] **Step 4: Create the thin controller wrapper**

Create `operator/controller/tool.go`:

```go
package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

// KapeToolReconciler is the thin controller-runtime adapter for KapeTool.
type KapeToolReconciler struct {
	inner *reconcile.ToolReconciler
}

// NewKapeToolReconciler creates a KapeToolReconciler.
func NewKapeToolReconciler(inner *reconcile.ToolReconciler) *KapeToolReconciler {
	return &KapeToolReconciler{inner: inner}
}

// Reconcile implements reconcile.Reconciler.
func (r *KapeToolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.inner.Reconcile(ctx, req.NamespacedName)
}

// SetupToolReconciler registers the KapeTool reconciler with the controller manager.
func SetupToolReconciler(mgr manager.Manager, inner *reconcile.ToolReconciler, maxConcurrent int) error {
	r := NewKapeToolReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeTool{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
```

- [ ] **Step 5: Run tests — verify they pass**

```bash
cd operator && go test ./controller/reconcile/... -run TestToolReconciler -v
```

Expected: all 4 tests PASS.

- [ ] **Step 6: Commit**

```bash
cd operator && git add controller/reconcile/tool.go controller/reconcile/tool_test.go controller/tool.go
git commit -m "feat(operator): add KapeToolReconciler (memory/mcp/event-publish)"
```

---

## Task 2: KapeSchemaReconciler

**Files:**
- Create: `operator/controller/reconcile/schema.go`
- Create: `operator/controller/reconcile/schema_test.go`
- Create: `operator/controller/schema.go`

- [ ] **Step 1: Write the failing tests**

Create `operator/controller/reconcile/schema_test.go`:

```go
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
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
cd operator && go test ./controller/reconcile/... -run TestSchemaReconciler -v 2>&1 | head -20
```

Expected: compile error — `reconcile.NewSchemaReconciler` undefined.

- [ ] **Step 3: Implement SchemaReconciler**

Create `operator/controller/reconcile/schema.go`:

```go
package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

const schemaFinalizer = "kape.io/schema-protection"

// SchemaReconciler performs the full reconcile logic for KapeSchema.
type SchemaReconciler struct {
	schemas ports.SchemaRepository
}

// NewSchemaReconciler creates a SchemaReconciler.
func NewSchemaReconciler(schemas ports.SchemaRepository) *SchemaReconciler {
	return &SchemaReconciler{schemas: schemas}
}

// Reconcile implements the KapeSchema reconcile loop.
func (r *SchemaReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	schema, err := r.schemas.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeSchema: %w", err)
	}
	if schema == nil {
		return ctrl.Result{}, nil
	}

	// 1. Validate spec.jsonSchema
	if err := validateJSONSchema(schema); err != nil {
		schema.Status.Conditions = setCondition(schema.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidSchema",
			Message: err.Error(),
		})
		_ = r.schemas.UpdateStatus(ctx, schema)
		return ctrl.Result{}, nil // terminal
	}

	// 2. Manage finalizer
	if err := r.schemas.AddFinalizer(ctx, schema, schemaFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
	}

	// 3. Handle deletion
	if !schema.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, schema)
	}

	// 4. Compute and write schemaHash
	hash, err := computeSchemaHash(schema)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("computing schema hash: %w", err)
	}
	schema.Status.SchemaHash = hash

	// 5. Set Ready=True
	schema.Status.Conditions = setCondition(schema.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Valid",
		Message: "JSON Schema validated successfully",
	})
	if err := r.schemas.UpdateStatus(ctx, schema); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SchemaReconciler) handleDeletion(ctx context.Context, schema *v1alpha1.KapeSchema) (ctrl.Result, error) {
	handlers, err := r.schemas.ListHandlersBySchemaRef(ctx, schema.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing handlers: %w", err)
	}
	if len(handlers) > 0 {
		names := make([]string, 0, len(handlers))
		for _, h := range handlers {
			names = append(names, h.Name)
		}
		schema.Status.Conditions = setCondition(schema.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ReferencedByHandlers",
			Message: fmt.Sprintf("Cannot delete: referenced by handlers: [%s]", strings.Join(names, ", ")),
		})
		_ = r.schemas.UpdateStatus(ctx, schema)
		return ctrl.Result{}, nil // blocked — no requeue; re-triggered on handler deletion
	}
	if err := r.schemas.RemoveFinalizer(ctx, schema, schemaFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func validateJSONSchema(schema *v1alpha1.KapeSchema) error {
	js := schema.Spec.JSONSchema
	if js.Type != "object" {
		return fmt.Errorf("spec.jsonSchema.type must be 'object', got %q", js.Type)
	}
	for _, req := range js.Required {
		if _, ok := js.Properties[req]; !ok {
			return fmt.Errorf("required field %q not found in properties", req)
		}
	}
	return nil
}

func computeSchemaHash(schema *v1alpha1.KapeSchema) (string, error) {
	b, err := json.Marshal(schema.Spec)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}
```

- [ ] **Step 4: Create the thin controller wrapper**

Create `operator/controller/schema.go`:

```go
package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

// KapeSchemaReconciler is the thin controller-runtime adapter for KapeSchema.
type KapeSchemaReconciler struct {
	inner *reconcile.SchemaReconciler
}

// NewKapeSchemaReconciler creates a KapeSchemaReconciler.
func NewKapeSchemaReconciler(inner *reconcile.SchemaReconciler) *KapeSchemaReconciler {
	return &KapeSchemaReconciler{inner: inner}
}

// Reconcile implements reconcile.Reconciler.
func (r *KapeSchemaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.inner.Reconcile(ctx, req.NamespacedName)
}

// SetupSchemaReconciler registers the KapeSchema reconciler with the controller manager.
func SetupSchemaReconciler(mgr manager.Manager, inner *reconcile.SchemaReconciler, maxConcurrent int) error {
	r := NewKapeSchemaReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeSchema{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
```

- [ ] **Step 5: Run tests — verify they pass**

```bash
cd operator && go test ./controller/reconcile/... -run TestSchemaReconciler -v
```

Expected: all 4 tests PASS.

- [ ] **Step 6: Commit**

```bash
cd operator && git add controller/reconcile/schema.go controller/reconcile/schema_test.go controller/schema.go
git commit -m "feat(operator): add KapeSchemaReconciler (validation, finalizer, schemaHash)"
```

---

## Task 3: KapeHandlerReconciler — full rewrite

**Files:**
- Replace: `operator/controller/reconcile/handler.go`
- Create: `operator/controller/reconcile/handler_test.go`

- [ ] **Step 1: Write the failing tests**

Create `operator/controller/reconcile/handler_test.go`:

```go
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
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
cd operator && go test ./controller/reconcile/... -run TestHandlerReconciler -v 2>&1 | head -30
```

Expected: compile error — `reconcile.NewHandlerReconciler` with new signature undefined.

- [ ] **Step 3: Replace handler.go with full 12-step implementation**

Replace the entire contents of `operator/controller/reconcile/handler.go`:

```go
package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

// HandlerReconciler performs the full 12-step reconcile logic for KapeHandler.
type HandlerReconciler struct {
	handlers        ports.HandlerRepository
	schemas         ports.SchemaRepository
	tools           ports.ToolRepository
	configMaps      ports.ConfigMapPort
	serviceAccounts ports.ServiceAccountPort
	deployments     ports.DeploymentPort
	scaledObjects   ports.ScaledObjectPort
	tomlRenderer    ports.TOMLRenderer
	kapeConfig      ports.KapeConfigLoader
}

// NewHandlerReconciler creates a HandlerReconciler with all required dependencies.
func NewHandlerReconciler(
	handlers ports.HandlerRepository,
	schemas ports.SchemaRepository,
	tools ports.ToolRepository,
	configMaps ports.ConfigMapPort,
	serviceAccounts ports.ServiceAccountPort,
	deployments ports.DeploymentPort,
	scaledObjects ports.ScaledObjectPort,
	tomlRenderer ports.TOMLRenderer,
	kapeConfig ports.KapeConfigLoader,
) *HandlerReconciler {
	return &HandlerReconciler{
		handlers:        handlers,
		schemas:         schemas,
		tools:           tools,
		configMaps:      configMaps,
		serviceAccounts: serviceAccounts,
		deployments:     deployments,
		scaledObjects:   scaledObjects,
		tomlRenderer:    tomlRenderer,
		kapeConfig:      kapeConfig,
	}
}

// Reconcile implements the full 12-step KapeHandler reconcile loop.
func (r *HandlerReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("handler", key)

	// Step 1: Fetch
	handler, err := r.handlers.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeHandler: %w", err)
	}
	if handler == nil {
		return ctrl.Result{}, nil
	}

	// Step 2: Dependency gate
	schema, resolvedTools, depsReady, gateMsg, gateReason, err := r.validateDependencies(ctx, handler)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !depsReady {
		handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
			Type:    "DependenciesReady",
			Status:  metav1.ConditionFalse,
			Reason:  gateReason,
			Message: gateMsg,
		})
		handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
			Type:   "Ready",
			Status: metav1.ConditionFalse,
			Reason: "DependenciesNotReady",
		})
		_ = r.handlers.UpdateStatus(ctx, handler)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
		Type:   "DependenciesReady",
		Status: metav1.ConditionTrue,
		Reason: "Ready",
	})

	// Step 3: Validate scaling
	if handler.Spec.Scaling != nil && handler.Spec.Scaling.ScaleToZero && handler.Spec.Scaling.MinReplicas >= 1 {
		handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
			Type:    "ScalingConfigured",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidScalingConfig",
			Message: "scaleToZero: true requires minReplicas: 0",
		})
		_ = r.handlers.UpdateStatus(ctx, handler)
		return ctrl.Result{}, nil // terminal
	}

	// Step 4: Compute hashes
	rolloutHash, err := computeRolloutHash(handler, schema, resolvedTools)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("computing rollout hash: %w", err)
	}
	consumerName := strings.ReplaceAll(handler.Spec.Trigger.Type, ".", "-")

	// Step 5: Load config and render settings.toml
	cfg, err := r.kapeConfig.Load(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading kape-config: %w", err)
	}
	tomlContent, err := r.tomlRenderer.Render(handler, schema, resolvedTools, cfg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("rendering settings.toml: %w", err)
	}
	if err := r.configMaps.Ensure(ctx, handler, tomlContent); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ConfigMap: %w", err)
	}
	log.V(1).Info("ConfigMap reconciled")

	// Step 6: Ensure ServiceAccount
	if err := r.serviceAccounts.Ensure(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ServiceAccount: %w", err)
	}

	// Step 7: Ensure Deployment (with sidecar injection)
	if err := r.deployments.Ensure(ctx, handler, cfg, rolloutHash, resolvedTools); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring Deployment: %w", err)
	}
	log.V(1).Info("Deployment reconciled", "rolloutHash", rolloutHash)

	// Step 8: Ensure KEDA ScaledObject
	soKey := types.NamespacedName{Name: "kape-handler-" + handler.Name, Namespace: handler.Namespace}
	existingConsumer, soFound, err := r.scaledObjects.GetConsumerName(ctx, soKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading ScaledObject: %w", err)
	}
	if soFound && existingConsumer != consumerName {
		// trigger.type changed — delete and recreate
		if err := r.scaledObjects.Delete(ctx, soKey); err != nil {
			return ctrl.Result{}, fmt.Errorf("deleting stale ScaledObject: %w", err)
		}
	}
	if err := r.scaledObjects.Ensure(ctx, handler, consumerName, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ScaledObject: %w", err)
	}

	// Step 9: Sync labels
	labels := map[string]string{"kape.io/schema-ref": handler.Spec.SchemaRef}
	for _, t := range handler.Spec.Tools {
		labels["kape.io/tool-ref-"+t.Ref] = "true"
	}
	if err := r.handlers.SyncLabels(ctx, handler, labels); err != nil {
		log.Error(err, "failed to sync labels")
	}

	// Step 10: Refresh handler after label patch
	handler, err = r.handlers.Get(ctx, key)
	if err != nil || handler == nil {
		return ctrl.Result{}, err
	}

	// Step 11: Read Deployment status → build conditions
	depKey := types.NamespacedName{Name: "kape-handler-" + handler.Name, Namespace: handler.Namespace}
	depStatus, depFound, err := r.deployments.GetStatus(ctx, depKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading Deployment status: %w", err)
	}
	handler.Status.Conditions = buildHandlerConditions(depStatus, depFound, handler.Status.Conditions)
	if depFound && depStatus != nil {
		handler.Status.Replicas = depStatus.ReadyReplicas
	}

	// Step 12: Patch status
	if err := r.handlers.UpdateStatus(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

// validateDependencies checks KapeSchema + KapeTools readiness. Returns resolved objects on success.
func (r *HandlerReconciler) validateDependencies(ctx context.Context, handler *v1alpha1.KapeHandler) (
	schema *v1alpha1.KapeSchema,
	tools []v1alpha1.KapeTool,
	ready bool,
	message, reason string,
	err error,
) {
	// Check schema
	schemaKey := types.NamespacedName{Name: handler.Spec.SchemaRef, Namespace: handler.Namespace}
	schema, err = r.schemas.Get(ctx, schemaKey)
	if err != nil {
		return nil, nil, false, "", "", fmt.Errorf("fetching KapeSchema: %w", err)
	}
	if schema == nil || !isConditionTrue(schema.Status.Conditions, "Ready") {
		msg := fmt.Sprintf("KapeSchema %q not found or not ready", handler.Spec.SchemaRef)
		if schema != nil {
			if c := findCond(schema.Status.Conditions, "Ready"); c != nil {
				msg = c.Message
			}
		}
		return nil, nil, false, msg, "KapeSchemaInvalid", nil
	}

	// Check tools
	tools = make([]v1alpha1.KapeTool, 0, len(handler.Spec.Tools))
	for _, ref := range handler.Spec.Tools {
		toolKey := types.NamespacedName{Name: ref.Ref, Namespace: handler.Namespace}
		tool, err := r.tools.Get(ctx, toolKey)
		if err != nil {
			return nil, nil, false, "", "", fmt.Errorf("fetching KapeTool %q: %w", ref.Ref, err)
		}
		if tool == nil || !isConditionTrue(tool.Status.Conditions, "Ready") {
			msg := fmt.Sprintf("KapeTool %q not found or not ready", ref.Ref)
			if tool != nil {
				if c := findCond(tool.Status.Conditions, "Ready"); c != nil {
					msg = fmt.Sprintf("KapeTool %q: %s", ref.Ref, c.Message)
				}
			}
			return nil, nil, false, msg, "KapeToolNotReady", nil
		}
		tools = append(tools, *tool)
	}
	return schema, tools, true, "", "", nil
}

func computeRolloutHash(handler *v1alpha1.KapeHandler, schema *v1alpha1.KapeSchema, tools []v1alpha1.KapeTool) (string, error) {
	h := sha256.New()
	for _, item := range []interface{}{handler.Spec, schema.Spec} {
		b, err := json.Marshal(item)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	for _, t := range tools {
		b, err := json.Marshal(t.Spec)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func buildHandlerConditions(depStatus *appsv1.DeploymentStatus, depFound bool, existing []metav1.Condition) []metav1.Condition {
	deploymentAvailable := metav1.Condition{Type: "DeploymentAvailable"}
	ready := metav1.Condition{Type: "Ready"}

	if !depFound {
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "DeploymentNotFound"
		ready.Status = metav1.ConditionFalse
		ready.Reason = "DeploymentNotFound"
	} else if depStatus == nil || depStatus.ReadyReplicas == 0 {
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "MinimumReplicasUnavailable"
		ready.Status = metav1.ConditionFalse
		ready.Reason = "DeploymentUnavailable"
	} else {
		deploymentAvailable.Status = metav1.ConditionTrue
		deploymentAvailable.Reason = "Available"
		deploymentAvailable.Message = fmt.Sprintf("%d/%d replicas ready", depStatus.ReadyReplicas, depStatus.Replicas)
		ready.Status = metav1.ConditionTrue
		ready.Reason = "Ready"
	}

	existing = setCondition(existing, deploymentAvailable)
	existing = setCondition(existing, ready)
	return existing
}

func isConditionTrue(conditions []metav1.Condition, condType string) bool {
	c := findCond(conditions, condType)
	return c != nil && c.Status == metav1.ConditionTrue
}

func findCond(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
cd operator && go test ./controller/reconcile/... -run TestHandlerReconciler -v
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Run all reconcile tests**

```bash
cd operator && go test ./controller/reconcile/... -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
cd operator && git add controller/reconcile/handler.go controller/reconcile/handler_test.go
git commit -m "feat(operator): replace KapeHandlerReconciler with full 12-step implementation"
```

---

## Task 4: Cross-resource watches + update SetupHandlerReconciler

**Files:**
- Create: `operator/controller/watches.go`
- Modify: `operator/controller/handler.go`

- [ ] **Step 1: Create watch map functions**

Create `operator/controller/watches.go`:

```go
package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// MapToolToHandlers maps a KapeTool change to the KapeHandlers that reference it.
// Used as a secondary watch: KapeTool changes re-enqueue referencing KapeHandlers.
func MapToolToHandlers(c client.Client) func(ctx context.Context, obj client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		tool, ok := obj.(*v1alpha1.KapeTool)
		if !ok {
			return nil
		}
		var handlerList v1alpha1.KapeHandlerList
		if err := c.List(ctx, &handlerList, client.MatchingLabels{
			fmt.Sprintf("kape.io/tool-ref-%s", tool.Name): "true",
		}); err != nil {
			return nil
		}
		requests := make([]reconcile.Request, 0, len(handlerList.Items))
		for _, h := range handlerList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: h.Name, Namespace: h.Namespace},
			})
		}
		return requests
	}
}

// MapSchemaToHandlers maps a KapeSchema change to the KapeHandlers that reference it.
// Used as a secondary watch: KapeSchema schemaHash changes re-enqueue referencing KapeHandlers.
func MapSchemaToHandlers(c client.Client) func(ctx context.Context, obj client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		schema, ok := obj.(*v1alpha1.KapeSchema)
		if !ok {
			return nil
		}
		var handlerList v1alpha1.KapeHandlerList
		if err := c.List(ctx, &handlerList, client.MatchingLabels{
			"kape.io/schema-ref": schema.Name,
		}); err != nil {
			return nil
		}
		requests := make([]reconcile.Request, 0, len(handlerList.Items))
		for _, h := range handlerList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: h.Name, Namespace: h.Namespace},
			})
		}
		return requests
	}
}
```

- [ ] **Step 2: Update SetupHandlerReconciler with secondary watches**

Replace the full contents of `operator/controller/handler.go`:

```go
package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

// KapeHandlerReconciler is the thin controller-runtime adapter for KapeHandler.
type KapeHandlerReconciler struct {
	inner *reconcile.HandlerReconciler
}

// NewKapeHandlerReconciler creates a KapeHandlerReconciler.
func NewKapeHandlerReconciler(inner *reconcile.HandlerReconciler) *KapeHandlerReconciler {
	return &KapeHandlerReconciler{inner: inner}
}

// Reconcile implements reconcile.Reconciler.
func (r *KapeHandlerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.inner.Reconcile(ctx, req.NamespacedName)
}

// SetupHandlerReconciler registers the KapeHandler reconciler with secondary watches for
// KapeTool and KapeSchema changes.
func SetupHandlerReconciler(mgr manager.Manager, inner *reconcile.HandlerReconciler, maxConcurrent int) error {
	r := NewKapeHandlerReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeHandler{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Watches(&v1alpha1.KapeTool{}, handler.EnqueueRequestsFromMapFunc(MapToolToHandlers(mgr.GetClient()))).
		Watches(&v1alpha1.KapeSchema{}, handler.EnqueueRequestsFromMapFunc(MapSchemaToHandlers(mgr.GetClient()))).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
```

- [ ] **Step 3: Verify compilation**

```bash
cd operator && go build ./controller/...
```

Expected: exits 0.

- [ ] **Step 4: Commit**

```bash
cd operator && git add controller/watches.go controller/handler.go
git commit -m "feat(operator): add cross-resource watches (KapeTool/KapeSchema → KapeHandler)"
```

---

## Task 5: Rewire main.go

**Files:**
- Replace: `operator/cmd/main.go`

- [ ] **Step 1: Replace main.go with full three-reconciler wiring**

Replace the full contents of `operator/cmd/main.go`:

```go
package main

import (
	"flag"
	"os"

	"github.com/peterbourgon/ff/v3"
	"github.com/peterbourgon/ff/v3/ffyaml"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	tomlrenderer "github.com/kape-io/kape/operator/infra/toml"
	kapecontroller "github.com/kape-io/kape/operator/controller"
	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

type config struct {
	MetricsAddr             string
	HealthProbeAddr         string
	LeaderElect             bool
	MaxConcurrentReconciles int
	KapeConfigNamespace     string
	KapeConfigName          string
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	log := ctrl.Log.WithName("main")

	fs := flag.NewFlagSet("kape-operator", flag.ExitOnError)
	cfg := &config{}
	fs.StringVar(&cfg.MetricsAddr, "metrics-bind-address", ":8080", "Address for the metrics endpoint")
	fs.StringVar(&cfg.HealthProbeAddr, "health-probe-bind-address", ":8081", "Address for the health probe endpoint")
	fs.BoolVar(&cfg.LeaderElect, "leader-elect", true, "Enable leader election")
	fs.IntVar(&cfg.MaxConcurrentReconciles, "max-concurrent-reconciles", 3, "Max parallel reconciles per controller")
	fs.StringVar(&cfg.KapeConfigNamespace, "kape-config-namespace", "kape-system", "Namespace of the kape-config ConfigMap")
	fs.StringVar(&cfg.KapeConfigName, "kape-config-name", "kape-config", "Name of the kape-config ConfigMap")

	if err := ff.Parse(fs, os.Args[1:],
		ff.WithEnvVarPrefix("KAPE_OPERATOR"),
		ff.WithConfigFileFlag("config"),
		ff.WithConfigFileParser(ffyaml.Parser),
		ff.WithAllowMissingConfigFile(true),
	); err != nil {
		log.Error(err, "parsing config")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: cfg.MetricsAddr},
		HealthProbeBindAddress: cfg.HealthProbeAddr,
		LeaderElection:         cfg.LeaderElect,
		LeaderElectionID:       "kape-operator-leader-election",
	})
	if err != nil {
		log.Error(err, "creating manager")
		os.Exit(1)
	}

	k8sClient := mgr.GetClient()

	// Shared adapters
	cfgLoader    := k8sadapters.NewKapeConfigLoader(k8sClient, cfg.KapeConfigNamespace, cfg.KapeConfigName)
	handlerRepo  := k8sadapters.NewHandlerRepository(k8sClient)
	schemaRepo   := k8sadapters.NewSchemaRepository(k8sClient)
	toolRepo     := k8sadapters.NewToolRepository(k8sClient)
	configMapAdapt  := k8sadapters.NewConfigMapAdapter(k8sClient)
	saAdapt         := k8sadapters.NewServiceAccountAdapter(k8sClient)
	deployAdapt     := k8sadapters.NewDeploymentAdapter(k8sClient)
	statefulSetAdapt := k8sadapters.NewStatefulSetAdapter(k8sClient)
	scaledObjectAdapt := k8sadapters.NewScaledObjectAdapter(k8sClient)
	renderer        := tomlrenderer.NewRenderer()

	// KapeToolReconciler
	toolRec := reconcilehandler.NewToolReconciler(toolRepo, statefulSetAdapt, cfgLoader)
	if err := kapecontroller.SetupToolReconciler(mgr, toolRec, cfg.MaxConcurrentReconciles); err != nil {
		log.Error(err, "setting up KapeTool controller")
		os.Exit(1)
	}

	// KapeSchemaReconciler
	schemaRec := reconcilehandler.NewSchemaReconciler(schemaRepo)
	if err := kapecontroller.SetupSchemaReconciler(mgr, schemaRec, cfg.MaxConcurrentReconciles); err != nil {
		log.Error(err, "setting up KapeSchema controller")
		os.Exit(1)
	}

	// KapeHandlerReconciler (full Phase 6 implementation)
	handlerRec := reconcilehandler.NewHandlerReconciler(
		handlerRepo,
		schemaRepo,
		toolRepo,
		configMapAdapt,
		saAdapt,
		deployAdapt,
		scaledObjectAdapt,
		renderer,
		cfgLoader,
	)
	if err := kapecontroller.SetupHandlerReconciler(mgr, handlerRec, cfg.MaxConcurrentReconciles); err != nil {
		log.Error(err, "setting up KapeHandler controller")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "setting up healthz check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "setting up readyz check")
		os.Exit(1)
	}

	log.Info("starting KAPE operator")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "running manager")
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Full build check**

```bash
cd operator && go build ./...
```

Expected: exits 0, zero errors.

- [ ] **Step 3: Run all tests**

```bash
cd operator && go test ./... -v 2>&1 | tail -30
```

Expected: all tests PASS.

- [ ] **Step 4: Commit**

```bash
cd operator && git add cmd/main.go
git commit -m "feat(operator): wire all three reconcilers in main.go (Phase 6 complete)"
```

---

## Plan B Complete — Phase 6 Acceptance Criteria

Verify against spec 0012 Phase 6:

- [ ] **AC1:** Apply KapeHandler + KapeTool (memory type) → `kubectl get statefulset -n kape-system` shows `kape-memory-{name}`; handler Deployment has `QDRANT_*` env vars injected via `kape-memory-*.Status.QdrantEndpoint`
- [ ] **AC2:** Apply KapeTool (mcp type) → `kubectl describe deployment kape-handler-{name}` shows `kapetool-{name}` sidecar container
- [ ] **AC3:** Apply KapeSchema → `kubectl get kapeschema {name} -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'` returns `True`
- [ ] **AC4:** Attempt to delete KapeSchema referenced by a KapeHandler → deletion blocked: `kubectl delete kapeschema {name}` hangs while finalizer prevents GC
- [ ] **AC5:** `kubectl get scaledobject -n kape-system` shows `kape-handler-{name}` with correct min/max replicas (requires KEDA installed on cluster)
