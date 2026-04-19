# Phase 6 — Plan A: Infrastructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build all infrastructure building blocks for Phase 6 — extended types, port interfaces, k8s adapters, updated TOML renderer, and sidecar-aware deployment builder.

**Architecture:** Single Go module at `operator/`. New files added to `infra/ports/`, `infra/k8s/`, updated `infra/toml/` and `domain/config/`. Plan B (reconcilers) depends on everything here.

**Tech Stack:** Go 1.24, controller-runtime v0.19, k8s.io/api v0.32, go-toml/v2, testify

---

## File Map

| File | Action | Purpose |
|---|---|---|
| `operator/go.mod` / `go.sum` | Modify | Add testify |
| `operator/domain/config/config.go` | Modify | Add QdrantVersion, QdrantStorageClass |
| `operator/infra/k8s/kapeconfig.go` | Modify | Parse qdrant.* keys from ConfigMap |
| `operator/infra/api/v1alpha1/kapetool_types.go` | Modify | Add QdrantEndpoint to KapeToolStatus |
| `operator/infra/api/v1alpha1/kapeschema_types.go` | Modify | Add SchemaHash to KapeSchemaStatus |
| `operator/infra/api/v1alpha1/zz_generated.deepcopy.go` | Modify | DeepCopy for new fields |
| `operator/infra/ports/tool.go` | Create | ToolRepository, StatefulSetPort, ScaledObjectPort interfaces |
| `operator/infra/ports/schema.go` | Create | SchemaRepository interface |
| `operator/infra/ports/handler.go` | Modify | Update TOMLRenderer + DeploymentPort signatures |
| `operator/infra/k8s/tool_repo.go` | Create | ToolRepository implementation |
| `operator/infra/k8s/schema_repo.go` | Create | SchemaRepository implementation |
| `operator/infra/k8s/statefulset.go` | Create | Qdrant StatefulSet + Service adapter |
| `operator/infra/k8s/scaledobject.go` | Create | KEDA ScaledObject adapter (unstructured) |
| `operator/infra/toml/renderer.go` | Modify | Add tool sections + schema section |
| `operator/infra/toml/renderer_test.go` | Create | TOML renderer tests |
| `operator/infra/k8s/deployment.go` | Modify | Inject kapetool sidecars per mcp-type tool |
| `operator/infra/k8s/deployment_test.go` | Create | Deployment sidecar injection tests |

---

## Task 1: Add testify + extend KapeConfig

**Files:**
- Modify: `operator/domain/config/config.go`
- Modify: `operator/infra/k8s/kapeconfig.go`

- [ ] **Step 1: Add testify**

```bash
cd operator && go get github.com/stretchr/testify@v1.10.0 && go mod tidy
```

Expected: `go.mod` gains `github.com/stretchr/testify v1.10.0`

- [ ] **Step 2: Add Qdrant fields to KapeConfig**

In `operator/domain/config/config.go`, add two fields to the `KapeConfig` struct after `NATSMonitoringEndpoint`:

```go
// Qdrant vector database
QdrantVersion      string
QdrantStorageClass string
```

Add defaults in `WithDefaults()` after the `NATSMonitoringEndpoint` block:

```go
if c.QdrantVersion == "" {
    c.QdrantVersion = "v1.14.0"
}
if c.QdrantStorageClass == "" {
    c.QdrantStorageClass = "standard"
}
```

- [ ] **Step 3: Parse Qdrant keys in kapeconfig.go**

In `operator/infra/k8s/kapeconfig.go`, inside the `cfg := domainconfig.KapeConfig{...}` literal, add:

```go
QdrantVersion:      cm.Data["qdrant.version"],
QdrantStorageClass: cm.Data["qdrant.storageClass"],
```

- [ ] **Step 4: Verify compilation**

```bash
cd operator && go build ./...
```

Expected: exits 0, no errors.

- [ ] **Step 5: Commit**

```bash
cd operator && git add domain/config/config.go infra/k8s/kapeconfig.go go.mod go.sum
git commit -m "feat(operator): add QdrantVersion/StorageClass to KapeConfig"
```

---

## Task 2: Extend CRD status types + update deepcopy

**Files:**
- Modify: `operator/infra/api/v1alpha1/kapetool_types.go`
- Modify: `operator/infra/api/v1alpha1/kapeschema_types.go`
- Modify: `operator/infra/api/v1alpha1/zz_generated.deepcopy.go`

- [ ] **Step 1: Add QdrantEndpoint to KapeToolStatus**

In `operator/infra/api/v1alpha1/kapetool_types.go`, in the `KapeToolStatus` struct, add after the `Conditions` field:

```go
// QdrantEndpoint is the Qdrant HTTP endpoint for memory-type tools.
// Written after the StatefulSet reaches ReadyReplicas >= 1.
// +optional
QdrantEndpoint string `json:"qdrantEndpoint,omitempty"`
```

- [ ] **Step 2: Add SchemaHash to KapeSchemaStatus**

In `operator/infra/api/v1alpha1/kapeschema_types.go`, in the `KapeSchemaStatus` struct, add after the `Conditions` field:

```go
// SchemaHash is a sha256 of spec.version + spec.jsonSchema.
// Changes to this field trigger a Deployment rollout of all referencing KapeHandlers.
// +optional
SchemaHash string `json:"schemaHash,omitempty"`
```

- [ ] **Step 3: Update deepcopy for KapeToolStatus**

In `operator/infra/api/v1alpha1/zz_generated.deepcopy.go`, find the `func (in *KapeToolStatus) DeepCopyInto(out *KapeToolStatus)` function and add the new field copy after the Conditions copy block:

```go
out.QdrantEndpoint = in.QdrantEndpoint
```

- [ ] **Step 4: Update deepcopy for KapeSchemaStatus**

In the same file, find `func (in *KapeSchemaStatus) DeepCopyInto(out *KapeSchemaStatus)` and add after the Conditions block:

```go
out.SchemaHash = in.SchemaHash
```

- [ ] **Step 5: Verify compilation**

```bash
cd operator && go build ./...
```

Expected: exits 0.

- [ ] **Step 6: Commit**

```bash
cd operator && git add infra/api/v1alpha1/
git commit -m "feat(operator): add QdrantEndpoint and SchemaHash to CRD status types"
```

---

## Task 3: New port interfaces

**Files:**
- Create: `operator/infra/ports/tool.go`
- Create: `operator/infra/ports/schema.go`
- Modify: `operator/infra/ports/handler.go`

- [ ] **Step 1: Create tool ports**

Create `operator/infra/ports/tool.go`:

```go
package ports

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// ToolRepository reads and writes KapeTool resources.
type ToolRepository interface {
	// Get fetches a KapeTool by namespaced name. Returns nil, nil when not found.
	Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeTool, error)

	// UpdateStatus persists status sub-resource changes.
	UpdateStatus(ctx context.Context, tool *v1alpha1.KapeTool) error

	// ListHandlersByToolRef returns all KapeHandlers with label kape.io/tool-ref-{toolName}=true.
	ListHandlersByToolRef(ctx context.Context, toolName string) ([]v1alpha1.KapeHandler, error)
}

// StatefulSetPort manages the Qdrant StatefulSet and headless Service for memory-type KapeTools.
type StatefulSetPort interface {
	// EnsureQdrant creates or patches the Qdrant StatefulSet and headless Service.
	EnsureQdrant(ctx context.Context, tool *v1alpha1.KapeTool, cfg domainconfig.KapeConfig) error

	// GetQdrantReadyReplicas returns the number of ready replicas. found=false when StatefulSet does not exist.
	GetQdrantReadyReplicas(ctx context.Context, key types.NamespacedName) (readyReplicas int32, found bool, err error)
}

// ScaledObjectPort manages KEDA ScaledObject resources for KapeHandlers.
type ScaledObjectPort interface {
	// Ensure creates or patches the KEDA ScaledObject for the handler.
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, consumerName string, cfg domainconfig.KapeConfig) error

	// GetConsumerName reads the NATS consumer name from the existing ScaledObject.
	// found=false when the ScaledObject does not exist.
	GetConsumerName(ctx context.Context, key types.NamespacedName) (consumerName string, found bool, err error)

	// Delete removes the ScaledObject. Returns nil when not found.
	Delete(ctx context.Context, key types.NamespacedName) error
}
```

- [ ] **Step 2: Create schema ports**

Create `operator/infra/ports/schema.go`:

```go
package ports

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// SchemaRepository reads and writes KapeSchema resources.
type SchemaRepository interface {
	// Get fetches a KapeSchema by namespaced name. Returns nil, nil when not found.
	Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeSchema, error)

	// UpdateStatus persists status sub-resource changes.
	UpdateStatus(ctx context.Context, schema *v1alpha1.KapeSchema) error

	// AddFinalizer adds the given finalizer string to the schema if not already present.
	AddFinalizer(ctx context.Context, schema *v1alpha1.KapeSchema, finalizer string) error

	// RemoveFinalizer removes the given finalizer string from the schema.
	RemoveFinalizer(ctx context.Context, schema *v1alpha1.KapeSchema, finalizer string) error

	// ListHandlersBySchemaRef returns all KapeHandlers with label kape.io/schema-ref={schemaName}.
	ListHandlersBySchemaRef(ctx context.Context, schemaName string) ([]v1alpha1.KapeHandler, error)
}
```

- [ ] **Step 3: Update handler ports — TOMLRenderer and DeploymentPort signatures**

Replace the `TOMLRenderer` and `DeploymentPort` interfaces in `operator/infra/ports/handler.go` with updated signatures:

```go
// TOMLRenderer produces a settings.toml string from a KapeHandler, its resolved schema,
// its resolved tools, and platform config.
type TOMLRenderer interface {
	Render(
		handler *v1alpha1.KapeHandler,
		schema *v1alpha1.KapeSchema,
		tools []v1alpha1.KapeTool,
		cfg domainconfig.KapeConfig,
	) (string, error)
}

// DeploymentPort manages the handler Deployment.
type DeploymentPort interface {
	// Ensure creates or patches the handler Deployment with sidecar injection for mcp-type tools.
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, cfg domainconfig.KapeConfig, rolloutHash string, tools []v1alpha1.KapeTool) error

	// GetStatus reads the current Deployment status. found is false when the Deployment does not exist.
	GetStatus(ctx context.Context, key types.NamespacedName) (status *appsv1.DeploymentStatus, found bool, err error)
}
```

The full updated `operator/infra/ports/handler.go`:

```go
package ports

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// HandlerRepository reads and writes KapeHandler resources.
type HandlerRepository interface {
	Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeHandler, error)
	UpdateStatus(ctx context.Context, handler *v1alpha1.KapeHandler) error
	SyncLabels(ctx context.Context, handler *v1alpha1.KapeHandler, labels map[string]string) error
}

// ConfigMapPort manages the handler settings.toml ConfigMap.
type ConfigMapPort interface {
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, tomlContent string) error
}

// ServiceAccountPort manages the per-handler ServiceAccount.
type ServiceAccountPort interface {
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler) error
}

// DeploymentPort manages the handler Deployment.
type DeploymentPort interface {
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, cfg domainconfig.KapeConfig, rolloutHash string, tools []v1alpha1.KapeTool) error
	GetStatus(ctx context.Context, key types.NamespacedName) (status *appsv1.DeploymentStatus, found bool, err error)
}

// KapeConfigLoader reads the kape-config ConfigMap and returns operator platform config.
type KapeConfigLoader interface {
	Load(ctx context.Context) (domainconfig.KapeConfig, error)
}

// TOMLRenderer produces a settings.toml string from a KapeHandler spec and platform config.
type TOMLRenderer interface {
	Render(
		handler *v1alpha1.KapeHandler,
		schema *v1alpha1.KapeSchema,
		tools []v1alpha1.KapeTool,
		cfg domainconfig.KapeConfig,
	) (string, error)
}
```

- [ ] **Step 4: Verify compilation**

```bash
cd operator && go build ./...
```

Expected: compilation errors in `controller/reconcile/handler.go` and `infra/toml/renderer.go` because their signatures no longer match. That is expected — they will be fixed in Tasks 8 and Plan B.

- [ ] **Step 5: Commit**

```bash
cd operator && git add infra/ports/
git commit -m "feat(operator): add ToolRepository, SchemaRepository, ScaledObjectPort interfaces"
```

---

## Task 4: ToolRepository adapter

**Files:**
- Create: `operator/infra/k8s/tool_repo.go`
- Create: `operator/infra/k8s/tool_repo_test.go`

- [ ] **Step 1: Write the failing tests**

Create `operator/infra/k8s/tool_repo_test.go`:

```go
package k8s_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
)

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	return s
}

func TestToolRepository_Get_Found(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "grafana-mcp", Namespace: "kape-system"},
		Spec:       v1alpha1.KapeToolSpec{Type: "mcp"},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(tool).WithStatusSubresource(tool).Build()
	repo := k8sadapters.NewToolRepository(c)

	got, err := repo.Get(context.Background(), types.NamespacedName{Name: "grafana-mcp", Namespace: "kape-system"})

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "grafana-mcp", got.Name)
}

func TestToolRepository_Get_NotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme()).Build()
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
	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(tool).WithStatusSubresource(tool).Build()
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
	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(handler).Build()
	repo := k8sadapters.NewToolRepository(c)

	handlers, err := repo.ListHandlersByToolRef(context.Background(), "grafana-mcp")

	require.NoError(t, err)
	require.Len(t, handlers, 1)
	assert.Equal(t, "my-handler", handlers[0].Name)
}
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
cd operator && go test ./infra/k8s/... -run TestToolRepository -v 2>&1 | head -20
```

Expected: compile error — `NewToolRepository` undefined.

- [ ] **Step 3: Implement ToolRepository**

Create `operator/infra/k8s/tool_repo.go`:

```go
package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// ToolRepository implements ports.ToolRepository.
type ToolRepository struct {
	client client.Client
}

// NewToolRepository creates a new ToolRepository.
func NewToolRepository(c client.Client) *ToolRepository {
	return &ToolRepository{client: c}
}

// Get fetches a KapeTool by namespaced name. Returns nil, nil when not found.
func (r *ToolRepository) Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeTool, error) {
	var tool v1alpha1.KapeTool
	if err := r.client.Get(ctx, key, &tool); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return &tool, nil
}

// UpdateStatus persists the tool's status sub-resource using RetryOnConflict.
func (r *ToolRepository) UpdateStatus(ctx context.Context, tool *v1alpha1.KapeTool) error {
	key := types.NamespacedName{Name: tool.Name, Namespace: tool.Namespace}
	desired := tool.Status
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest v1alpha1.KapeTool
		if err := r.client.Get(ctx, key, &latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		latest.Status = desired
		return r.client.Status().Update(ctx, &latest)
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("updating KapeTool %s/%s status: %w", tool.Namespace, tool.Name, err)
	}
	return nil
}

// ListHandlersByToolRef returns KapeHandlers with label kape.io/tool-ref-{toolName}=true.
func (r *ToolRepository) ListHandlersByToolRef(ctx context.Context, toolName string) ([]v1alpha1.KapeHandler, error) {
	var list v1alpha1.KapeHandlerList
	if err := r.client.List(ctx, &list, client.MatchingLabels{
		"kape.io/tool-ref-" + toolName: "true",
	}); err != nil {
		return nil, fmt.Errorf("listing handlers by tool ref %q: %w", toolName, err)
	}
	return list.Items, nil
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
cd operator && go test ./infra/k8s/... -run TestToolRepository -v
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
cd operator && git add infra/k8s/tool_repo.go infra/k8s/tool_repo_test.go
git commit -m "feat(operator): add ToolRepository adapter"
```

---

## Task 5: SchemaRepository adapter

**Files:**
- Create: `operator/infra/k8s/schema_repo.go`
- Create: `operator/infra/k8s/schema_repo_test.go`

- [ ] **Step 1: Write the failing tests**

Create `operator/infra/k8s/schema_repo_test.go`:

```go
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
	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(schema).WithStatusSubresource(schema).Build()
	repo := k8sadapters.NewSchemaRepository(c)

	got, err := repo.Get(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "my-schema", got.Name)
}

func TestSchemaRepository_Get_NotFound(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	repo := k8sadapters.NewSchemaRepository(c)

	got, err := repo.Get(context.Background(), types.NamespacedName{Name: "missing", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSchemaRepository_UpdateStatus(t *testing.T) {
	schema := &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "my-schema", Namespace: "kape-system"},
	}
	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(schema).WithStatusSubresource(schema).Build()
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
	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(schema).Build()
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
	c := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(handler).Build()
	repo := k8sadapters.NewSchemaRepository(c)

	handlers, err := repo.ListHandlersBySchemaRef(context.Background(), "my-schema")

	require.NoError(t, err)
	require.Len(t, handlers, 1)
	assert.Equal(t, "my-handler", handlers[0].Name)
}
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
cd operator && go test ./infra/k8s/... -run TestSchemaRepository -v 2>&1 | head -20
```

Expected: compile error — `NewSchemaRepository` undefined.

- [ ] **Step 3: Implement SchemaRepository**

Create `operator/infra/k8s/schema_repo.go`:

```go
package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// SchemaRepository implements ports.SchemaRepository.
type SchemaRepository struct {
	client client.Client
}

// NewSchemaRepository creates a new SchemaRepository.
func NewSchemaRepository(c client.Client) *SchemaRepository {
	return &SchemaRepository{client: c}
}

// Get fetches a KapeSchema by namespaced name. Returns nil, nil when not found.
func (r *SchemaRepository) Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeSchema, error) {
	var schema v1alpha1.KapeSchema
	if err := r.client.Get(ctx, key, &schema); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return &schema, nil
}

// UpdateStatus persists the schema's status sub-resource using RetryOnConflict.
func (r *SchemaRepository) UpdateStatus(ctx context.Context, schema *v1alpha1.KapeSchema) error {
	key := types.NamespacedName{Name: schema.Name, Namespace: schema.Namespace}
	desired := schema.Status
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest v1alpha1.KapeSchema
		if err := r.client.Get(ctx, key, &latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		latest.Status = desired
		return r.client.Status().Update(ctx, &latest)
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("updating KapeSchema %s/%s status: %w", schema.Namespace, schema.Name, err)
	}
	return nil
}

// AddFinalizer adds the given finalizer to the schema if not already present.
func (r *SchemaRepository) AddFinalizer(ctx context.Context, schema *v1alpha1.KapeSchema, finalizer string) error {
	if controllerutil.ContainsFinalizer(schema, finalizer) {
		return nil
	}
	patch := client.MergeFrom(schema.DeepCopy())
	controllerutil.AddFinalizer(schema, finalizer)
	if err := r.client.Patch(ctx, schema, patch); err != nil {
		return fmt.Errorf("adding finalizer to KapeSchema %s/%s: %w", schema.Namespace, schema.Name, err)
	}
	return nil
}

// RemoveFinalizer removes the given finalizer from the schema.
func (r *SchemaRepository) RemoveFinalizer(ctx context.Context, schema *v1alpha1.KapeSchema, finalizer string) error {
	if !controllerutil.ContainsFinalizer(schema, finalizer) {
		return nil
	}
	patch := client.MergeFrom(schema.DeepCopy())
	controllerutil.RemoveFinalizer(schema, finalizer)
	if err := r.client.Patch(ctx, schema, patch); err != nil {
		return fmt.Errorf("removing finalizer from KapeSchema %s/%s: %w", schema.Namespace, schema.Name, err)
	}
	return nil
}

// ListHandlersBySchemaRef returns KapeHandlers with label kape.io/schema-ref={schemaName}.
func (r *SchemaRepository) ListHandlersBySchemaRef(ctx context.Context, schemaName string) ([]v1alpha1.KapeHandler, error) {
	var list v1alpha1.KapeHandlerList
	if err := r.client.List(ctx, &list, client.MatchingLabels{
		"kape.io/schema-ref": schemaName,
	}); err != nil {
		return nil, fmt.Errorf("listing handlers by schema ref %q: %w", schemaName, err)
	}
	return list.Items, nil
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
cd operator && go test ./infra/k8s/... -run TestSchemaRepository -v
```

Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
cd operator && git add infra/k8s/schema_repo.go infra/k8s/schema_repo_test.go
git commit -m "feat(operator): add SchemaRepository adapter"
```

---

## Task 6: StatefulSet adapter (Qdrant)

**Files:**
- Create: `operator/infra/k8s/statefulset.go`
- Create: `operator/infra/k8s/statefulset_test.go`

- [ ] **Step 1: Write the failing tests**

Create `operator/infra/k8s/statefulset_test.go`:

```go
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

func newStatefulSetScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func TestStatefulSetAdapter_EnsureQdrant_Creates(t *testing.T) {
	tool := &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: "karpenter-memory", Namespace: "kape-system", UID: "uid-1"},
		Spec:       v1alpha1.KapeToolSpec{Type: "memory", Memory: &v1alpha1.MemorySpec{Backend: "qdrant", DistanceMetric: "cosine"}},
	}
	s := newStatefulSetScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(tool).Build()
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
	s := newStatefulSetScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()
	adapter := k8sadapters.NewStatefulSetAdapter(c)

	ready, found, err := adapter.GetQdrantReadyReplicas(context.Background(),
		types.NamespacedName{Name: "kape-memory-missing", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, int32(0), ready)
}
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
cd operator && go test ./infra/k8s/... -run TestStatefulSetAdapter -v 2>&1 | head -20
```

Expected: compile error — `NewStatefulSetAdapter` undefined.

- [ ] **Step 3: Implement StatefulSet adapter**

Create `operator/infra/k8s/statefulset.go`:

```go
package k8s

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// StatefulSetAdapter implements ports.StatefulSetPort.
type StatefulSetAdapter struct {
	client client.Client
}

// NewStatefulSetAdapter creates a new StatefulSetAdapter.
func NewStatefulSetAdapter(c client.Client) *StatefulSetAdapter {
	return &StatefulSetAdapter{client: c}
}

func qdrantName(toolName string) string { return "kape-memory-" + toolName }

// EnsureQdrant creates or patches the Qdrant StatefulSet and headless Service.
func (a *StatefulSetAdapter) EnsureQdrant(ctx context.Context, tool *v1alpha1.KapeTool, cfg domainconfig.KapeConfig) error {
	cfg = cfg.WithDefaults()
	if err := a.ensureStatefulSet(ctx, tool, cfg); err != nil {
		return err
	}
	return a.ensureService(ctx, tool)
}

func (a *StatefulSetAdapter) ensureStatefulSet(ctx context.Context, tool *v1alpha1.KapeTool, cfg domainconfig.KapeConfig) error {
	name := qdrantName(tool.Name)
	key := types.NamespacedName{Name: name, Namespace: tool.Namespace}
	desired := buildQdrantStatefulSet(tool, cfg)

	var existing appsv1.StatefulSet
	err := a.client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		return a.client.Create(ctx, &desired)
	}
	if err != nil {
		return fmt.Errorf("getting StatefulSet %s/%s: %w", tool.Namespace, name, err)
	}
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Spec.Template = desired.Spec.Template
	return a.client.Patch(ctx, &existing, patch)
}

func (a *StatefulSetAdapter) ensureService(ctx context.Context, tool *v1alpha1.KapeTool) error {
	name := qdrantName(tool.Name)
	key := types.NamespacedName{Name: name, Namespace: tool.Namespace}
	desired := buildQdrantService(tool)

	var existing corev1.Service
	err := a.client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		return a.client.Create(ctx, &desired)
	}
	if err != nil {
		return fmt.Errorf("getting Service %s/%s: %w", tool.Namespace, name, err)
	}
	return nil // Service spec is immutable for ClusterIP=None; existence is sufficient
}

// GetQdrantReadyReplicas returns ready replica count. found=false when StatefulSet not found.
func (a *StatefulSetAdapter) GetQdrantReadyReplicas(ctx context.Context, key types.NamespacedName) (int32, bool, error) {
	var sts appsv1.StatefulSet
	if err := a.client.Get(ctx, key, &sts); err != nil {
		if apierrors.IsNotFound(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("getting StatefulSet %s: %w", key, err)
	}
	return sts.Status.ReadyReplicas, true, nil
}

func buildQdrantStatefulSet(tool *v1alpha1.KapeTool, cfg domainconfig.KapeConfig) appsv1.StatefulSet {
	name := qdrantName(tool.Name)
	labels := map[string]string{"kape.io/qdrant": tool.Name}
	one := int32(1)
	storageClass := cfg.QdrantStorageClass

	sts := appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: tool.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: name,
			Replicas:    &one,
			Selector:    &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "qdrant",
						Image: "qdrant/qdrant:" + cfg.QdrantVersion,
						Ports: []corev1.ContainerPort{
							{Name: "http", ContainerPort: 6333, Protocol: corev1.ProtocolTCP},
							{Name: "grpc", ContainerPort: 6334, Protocol: corev1.ProtocolTCP},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "storage", MountPath: "/qdrant/storage"},
						},
					}},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: "storage"},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					StorageClassName: &storageClass,
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
				},
			}},
		},
	}
	setToolOwnerRef(tool, &sts.ObjectMeta)
	return sts
}

func buildQdrantService(tool *v1alpha1.KapeTool) corev1.Service {
	name := qdrantName(tool.Name)
	labels := map[string]string{"kape.io/qdrant": tool.Name}
	svc := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: tool.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  labels,
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 6333, Protocol: corev1.ProtocolTCP},
				{Name: "grpc", Port: 6334, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	setToolOwnerRef(tool, &svc.ObjectMeta)
	return svc
}

// setToolOwnerRef sets a KapeTool owner reference on the given object.
func setToolOwnerRef(tool *v1alpha1.KapeTool, obj *metav1.ObjectMeta) {
	controller := true
	blockOwnerDeletion := true
	obj.OwnerReferences = []metav1.OwnerReference{{
		APIVersion:         "kape.io/v1alpha1",
		Kind:               "KapeTool",
		Name:               tool.Name,
		UID:                tool.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}}
}
```

- [ ] **Step 4: Extract shared test scheme helper**

All `_test.go` files in `infra/k8s/` are in `package k8s_test`. Each defines its own scheme helper causing duplicate function errors. Extract them into one shared file.

Create `operator/infra/k8s/testhelpers_test.go`:

```go
package k8s_test

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}
```

Then replace every `newScheme()`, `newStatefulSetScheme()`, `newScaledObjectScheme()`, and `newDeploymentScheme()` call across all `_test.go` files in `infra/k8s/` with `newTestScheme()`, and delete the now-duplicate local helpers.

- [ ] **Step 5: Add missing imports to statefulset_test.go**

Update `statefulset_test.go` imports:

```go
import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
)
```

Also update `tool_repo_test.go` and `schema_repo_test.go` to import `"k8s.io/apimachinery/pkg/runtime"` (needed by `newScheme()`).

- [ ] **Step 6: Run tests — verify they pass**

```bash
cd operator && go test ./infra/k8s/... -run TestStatefulSetAdapter -v
```

Expected: all 2 tests PASS.

- [ ] **Step 7: Commit**

```bash
cd operator && git add infra/k8s/statefulset.go infra/k8s/statefulset_test.go infra/k8s/testhelpers_test.go
git commit -m "feat(operator): add StatefulSet adapter for Qdrant provisioning"
```

---

## Task 7: ScaledObject adapter (KEDA unstructured)

**Files:**
- Create: `operator/infra/k8s/scaledobject.go`
- Create: `operator/infra/k8s/scaledobject_test.go`

- [ ] **Step 1: Write the failing tests**

Create `operator/infra/k8s/scaledobject_test.go`:

```go
package k8s_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
)

func newScaledObjectScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	return s
}

func TestScaledObjectAdapter_EnsureAndGetConsumerName(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "my-handler", Namespace: "kape-system", UID: "uid-h"},
		Spec: v1alpha1.KapeHandlerSpec{
			Scaling: &v1alpha1.ScalingSpec{MinReplicas: 1, MaxReplicas: 5, NatsLagThreshold: 5, ScaleDownStabilizationSeconds: 60},
		},
	}
	c := fake.NewClientBuilder().WithScheme(newScaledObjectScheme()).Build()
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
	c := fake.NewClientBuilder().WithScheme(newScaledObjectScheme()).Build()
	adapter := k8sadapters.NewScaledObjectAdapter(c)

	_, found, err := adapter.GetConsumerName(context.Background(),
		types.NamespacedName{Name: "missing", Namespace: "kape-system"})
	require.NoError(t, err)
	assert.False(t, found)
}

func TestScaledObjectAdapter_Delete(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "my-handler", Namespace: "kape-system", UID: "uid-h"},
	}
	c := fake.NewClientBuilder().WithScheme(newScaledObjectScheme()).Build()
	adapter := k8sadapters.NewScaledObjectAdapter(c)
	cfg := domainconfig.KapeConfig{}

	_ = adapter.Ensure(context.Background(), handler, "consumer-1", cfg)

	err := adapter.Delete(context.Background(), types.NamespacedName{Name: "kape-handler-my-handler", Namespace: "kape-system"})
	require.NoError(t, err)

	_, found, _ := adapter.GetConsumerName(context.Background(),
		types.NamespacedName{Name: "kape-handler-my-handler", Namespace: "kape-system"})
	assert.False(t, found)
}
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
cd operator && go test ./infra/k8s/... -run TestScaledObjectAdapter -v 2>&1 | head -20
```

Expected: compile error — `NewScaledObjectAdapter` undefined.

- [ ] **Step 3: Implement ScaledObject adapter**

Create `operator/infra/k8s/scaledobject.go`:

```go
package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

var scaledObjectGVK = schema.GroupVersionKind{
	Group:   "keda.sh",
	Version: "v1alpha1",
	Kind:    "ScaledObject",
}

// ScaledObjectAdapter implements ports.ScaledObjectPort using unstructured resources.
// No kedacore/keda/v2 import is needed.
type ScaledObjectAdapter struct {
	client client.Client
}

// NewScaledObjectAdapter creates a new ScaledObjectAdapter.
func NewScaledObjectAdapter(c client.Client) *ScaledObjectAdapter {
	return &ScaledObjectAdapter{client: c}
}

// Ensure creates or patches the KEDA ScaledObject for the handler.
func (a *ScaledObjectAdapter) Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, consumerName string, cfg domainconfig.KapeConfig) error {
	cfg = cfg.WithDefaults()
	desired := buildScaledObject(handler, consumerName, cfg.NATSMonitoringEndpoint)
	key := types.NamespacedName{Name: desired.GetName(), Namespace: desired.GetNamespace()}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(scaledObjectGVK)
	err := a.client.Get(ctx, key, existing)
	if apierrors.IsNotFound(err) {
		return a.client.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("getting ScaledObject %s/%s: %w", handler.Namespace, desired.GetName(), err)
	}
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Object["spec"] = desired.Object["spec"]
	return a.client.Patch(ctx, existing, patch)
}

// GetConsumerName reads the NATS consumer name from the existing ScaledObject.
func (a *ScaledObjectAdapter) GetConsumerName(ctx context.Context, key types.NamespacedName) (string, bool, error) {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(scaledObjectGVK)
	if err := a.client.Get(ctx, key, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("getting ScaledObject %s: %w", key, err)
	}
	triggers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "triggers")
	if len(triggers) == 0 {
		return "", true, nil
	}
	triggerMap, ok := triggers[0].(map[string]interface{})
	if !ok {
		return "", true, nil
	}
	consumer, _, _ := unstructured.NestedString(triggerMap, "metadata", "consumer")
	return consumer, true, nil
}

// Delete removes the ScaledObject. Returns nil when not found.
func (a *ScaledObjectAdapter) Delete(ctx context.Context, key types.NamespacedName) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(scaledObjectGVK)
	obj.SetName(key.Name)
	obj.SetNamespace(key.Namespace)
	if err := a.client.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting ScaledObject %s: %w", key, err)
	}
	return nil
}

func buildScaledObject(handler *v1alpha1.KapeHandler, consumerName, natsEndpoint string) *unstructured.Unstructured {
	scaling := resolveScaling(handler.Spec.Scaling)
	minReplicas := int64(scaling.MinReplicas)
	if scaling.ScaleToZero {
		minReplicas = 0
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "keda.sh/v1alpha1",
			"kind":       "ScaledObject",
			"metadata": map[string]interface{}{
				"name":      "kape-handler-" + handler.Name,
				"namespace": handler.Namespace,
			},
			"spec": map[string]interface{}{
				"scaleTargetRef":  map[string]interface{}{"name": "kape-handler-" + handler.Name},
				"minReplicaCount": minReplicas,
				"maxReplicaCount": int64(scaling.MaxReplicas),
				"cooldownPeriod":  int64(scaling.ScaleDownStabilizationSeconds),
				"triggers": []interface{}{
					map[string]interface{}{
						"type": "nats-jetstream",
						"metadata": map[string]interface{}{
							"natsServerMonitoringEndpoint": natsEndpoint,
							"streamName":                   "kape-events",
							"consumer":                     consumerName,
							"lagThreshold":                 fmt.Sprintf("%d", scaling.NatsLagThreshold),
						},
					},
				},
			},
		},
	}
	setHandlerOwnerRefUnstructured(handler, obj)
	return obj
}

func resolveScaling(s *v1alpha1.ScalingSpec) v1alpha1.ScalingSpec {
	if s == nil {
		return v1alpha1.ScalingSpec{MinReplicas: 1, MaxReplicas: 10, NatsLagThreshold: 5, ScaleDownStabilizationSeconds: 60}
	}
	out := *s
	if out.MaxReplicas == 0 {
		out.MaxReplicas = 10
	}
	if out.NatsLagThreshold == 0 {
		out.NatsLagThreshold = 5
	}
	if out.ScaleDownStabilizationSeconds == 0 {
		out.ScaleDownStabilizationSeconds = 60
	}
	if !out.ScaleToZero && out.MinReplicas == 0 {
		out.MinReplicas = 1
	}
	return out
}

// setHandlerOwnerRefUnstructured sets a KapeHandler owner reference on an unstructured object.
func setHandlerOwnerRefUnstructured(handler *v1alpha1.KapeHandler, obj *unstructured.Unstructured) {
	controller := true
	blockOwnerDeletion := true
	obj.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion:         "kape.io/v1alpha1",
		Kind:               "KapeHandler",
		Name:               handler.Name,
		UID:                handler.UID,
		Controller:         &controller,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}})
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
cd operator && go test ./infra/k8s/... -run TestScaledObjectAdapter -v
```

Expected: all 3 tests PASS.

- [ ] **Step 5: Commit**

```bash
cd operator && git add infra/k8s/scaledobject.go infra/k8s/scaledobject_test.go
git commit -m "feat(operator): add ScaledObject adapter (KEDA unstructured)"
```

---

## Task 8: TOML renderer — add tool sections and schema section

**Files:**
- Modify: `operator/infra/toml/renderer.go`
- Create: `operator/infra/toml/renderer_test.go`

- [ ] **Step 1: Write the failing tests**

Create `operator/infra/toml/renderer_test.go`:

```go
package toml_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	tomlrenderer "github.com/kape-io/kape/operator/infra/toml"
)

func baseHandler() *v1alpha1.KapeHandler {
	return &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "test-handler", Namespace: "kape-system"},
		Spec: v1alpha1.KapeHandlerSpec{
			Trigger:   v1alpha1.TriggerSpec{Source: "alertmanager", Type: "kape.events.test"},
			LLM:       v1alpha1.LLMSpec{Provider: "anthropic", Model: "claude-3", SystemPrompt: "test prompt"},
			SchemaRef: "test-schema",
			Tools:     []v1alpha1.ToolRef{{Ref: "grafana-mcp"}, {Ref: "karpenter-memory"}},
			Actions:   []v1alpha1.ActionSpec{},
		},
	}
}

func baseSchema() *v1alpha1.KapeSchema {
	return &v1alpha1.KapeSchema{
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

func baseTools() []v1alpha1.KapeTool {
	auditEnabled := true
	return []v1alpha1.KapeTool{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana-mcp"},
			Spec: v1alpha1.KapeToolSpec{
				Type: "mcp",
				MCP: &v1alpha1.MCPSpec{
					Upstream:     v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://grafana:8080"},
					AllowedTools: []string{"grafana_*"},
					Audit:        &v1alpha1.AuditSpec{Enabled: &auditEnabled},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "karpenter-memory"},
			Spec:       v1alpha1.KapeToolSpec{Type: "memory"},
			Status: v1alpha1.KapeToolStatus{
				QdrantEndpoint: "http://kape-memory-karpenter-memory.kape-system:6333",
			},
		},
	}
}

func TestRenderer_IncludesMCPToolSection(t *testing.T) {
	r := tomlrenderer.NewRenderer()
	content, err := r.Render(baseHandler(), baseSchema(), baseTools(), domainconfig.KapeConfig{})
	require.NoError(t, err)

	assert.Contains(t, content, "[tools.grafana-mcp]")
	assert.Contains(t, content, `type = "mcp"`)
	assert.Contains(t, content, "sidecar_port = 8080")
	assert.Contains(t, content, `transport = "sse"`)
}

func TestRenderer_IncludesMemoryToolSection(t *testing.T) {
	r := tomlrenderer.NewRenderer()
	content, err := r.Render(baseHandler(), baseSchema(), baseTools(), domainconfig.KapeConfig{})
	require.NoError(t, err)

	assert.Contains(t, content, "[tools.karpenter-memory]")
	assert.Contains(t, content, `type = "memory"`)
	assert.Contains(t, content, "kape-memory-karpenter-memory.kape-system:6333")
}

func TestRenderer_IncludesSchemaSection(t *testing.T) {
	r := tomlrenderer.NewRenderer()
	content, err := r.Render(baseHandler(), baseSchema(), baseTools(), domainconfig.KapeConfig{})
	require.NoError(t, err)

	assert.Contains(t, content, "[schema]")
	assert.Contains(t, content, "decision")
}

func TestRenderer_MCPPortsAssignedPositionally(t *testing.T) {
	handler := baseHandler()
	handler.Spec.Tools = []v1alpha1.ToolRef{{Ref: "tool-a"}, {Ref: "tool-b"}}
	tools := []v1alpha1.KapeTool{
		{ObjectMeta: metav1.ObjectMeta{Name: "tool-a"}, Spec: v1alpha1.KapeToolSpec{Type: "mcp", MCP: &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://a:8080"}}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "tool-b"}, Spec: v1alpha1.KapeToolSpec{Type: "mcp", MCP: &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://b:8080"}}}},
	}
	r := tomlrenderer.NewRenderer()
	content, err := r.Render(handler, baseSchema(), tools, domainconfig.KapeConfig{})
	require.NoError(t, err)

	assert.True(t, strings.Contains(content, "sidecar_port = 8080") || strings.Contains(content, "sidecar_port = 8081"),
		"should assign ports 8080 and 8081 to two mcp tools")
}
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
cd operator && go test ./infra/toml/... -v 2>&1 | head -30
```

Expected: compile error — `Render` takes 2 args, called with 4.

- [ ] **Step 3: Update the TOML renderer**

Replace the full contents of `operator/infra/toml/renderer.go` with:

```go
package toml

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	gotoml "github.com/pelletier/go-toml/v2"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// Renderer implements ports.TOMLRenderer.
type Renderer struct{}

// NewRenderer returns a new Renderer.
func NewRenderer() *Renderer { return &Renderer{} }

// Render serialises a KapeHandler, its resolved KapeSchema, resolved KapeTools,
// and platform config into a settings.toml string.
func (r *Renderer) Render(
	handler *v1alpha1.KapeHandler,
	schema *v1alpha1.KapeSchema,
	tools []v1alpha1.KapeTool,
	cfg domainconfig.KapeConfig,
) (string, error) {
	cfg = cfg.WithDefaults()

	replayOnStartup := true
	if handler.Spec.Trigger.ReplayOnStartup != nil {
		replayOnStartup = *handler.Spec.Trigger.ReplayOnStartup
	}
	maxIterations := handler.Spec.LLM.MaxIterations
	if maxIterations == 0 {
		maxIterations = cfg.DefaultMaxIterations
	}

	consumerName := strings.ReplaceAll(handler.Spec.Trigger.Type, ".", "-")
	taskServiceEndpoint := fmt.Sprintf("http://kape-task-service.%s:8080", handler.Namespace)

	actions, err := buildActions(handler)
	if err != nil {
		return "", fmt.Errorf("building actions: %w", err)
	}

	toolSections := buildToolSections(handler, tools)

	schemaSection, err := buildSchemaSection(schema)
	if err != nil {
		return "", fmt.Errorf("building schema section: %w", err)
	}

	s := settingsTOML{
		Kape: kapeTOML{
			HandlerName:        handler.Name,
			HandlerNamespace:   handler.Namespace,
			ClusterName:        cfg.ClusterName,
			DryRun:             handler.Spec.DryRun,
			MaxIterations:      maxIterations,
			SchemaName:         handler.Spec.SchemaRef,
			ReplayOnStartup:    replayOnStartup,
			MaxEventAgeSeconds: handler.Spec.Trigger.MaxEventAgeSeconds,
		},
		LLM: llmTOML{
			Provider:     handler.Spec.LLM.Provider,
			Model:        handler.Spec.LLM.Model,
			SystemPrompt: handler.Spec.LLM.SystemPrompt,
		},
		NATS: natsTOML{
			Subject:  handler.Spec.Trigger.Type,
			Consumer: consumerName,
			Stream:   "kape-events",
		},
		TaskService: taskServiceTOML{Endpoint: taskServiceEndpoint},
		OTEL:        otelTOML{Endpoint: "http://otel-collector.kape-system:4318", ServiceName: "kape-handler"},
		Tools:       toolSections,
		Schema:      schemaSection,
		Actions:     actions,
	}

	var buf bytes.Buffer
	if err := gotoml.NewEncoder(&buf).Encode(s); err != nil {
		return "", fmt.Errorf("encoding settings.toml: %w", err)
	}
	return buf.String(), nil
}

func buildToolSections(handler *v1alpha1.KapeHandler, tools []v1alpha1.KapeTool) map[string]toolTOML {
	toolMap := make(map[string]v1alpha1.KapeTool, len(tools))
	for _, t := range tools {
		toolMap[t.Name] = t
	}
	result := make(map[string]toolTOML, len(handler.Spec.Tools))
	mcpPort := 8080
	for _, ref := range handler.Spec.Tools {
		t, ok := toolMap[ref.Ref]
		if !ok {
			continue
		}
		switch t.Spec.Type {
		case "mcp":
			result[ref.Ref] = toolTOML{
				Type:        "mcp",
				SidecarPort: mcpPort,
				Transport:   t.Spec.MCP.Upstream.Transport,
			}
			mcpPort++
		case "memory":
			result[ref.Ref] = toolTOML{
				Type:           "memory",
				QdrantEndpoint: t.Status.QdrantEndpoint,
			}
		}
	}
	return result
}

func buildSchemaSection(schema *v1alpha1.KapeSchema) (schemaTOML, error) {
	b, err := json.Marshal(schema.Spec.JSONSchema)
	if err != nil {
		return schemaTOML{}, fmt.Errorf("marshalling schema: %w", err)
	}
	return schemaTOML{JSON: string(b)}, nil
}

func buildActions(handler *v1alpha1.KapeHandler) ([]actionTOML, error) {
	result := make([]actionTOML, 0, len(handler.Spec.Actions))
	for _, a := range handler.Spec.Actions {
		data, err := convertActionData(a)
		if err != nil {
			return nil, fmt.Errorf("action %q: %w", a.Name, err)
		}
		result = append(result, actionTOML{
			Name:      a.Name,
			Condition: a.Condition,
			Type:      a.Type,
			Data:      data,
		})
	}
	return result, nil
}

func convertActionData(a v1alpha1.ActionSpec) (map[string]interface{}, error) {
	if len(a.Data) == 0 {
		return nil, nil
	}
	result := make(map[string]interface{}, len(a.Data))
	for k, v := range a.Data {
		var val interface{}
		if err := json.Unmarshal(v.Raw, &val); err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		result[k] = val
	}
	return result, nil
}

// ─── internal TOML struct tree ─────────────────────────────────────────────────

type settingsTOML struct {
	Kape        kapeTOML            `toml:"kape"`
	LLM         llmTOML             `toml:"llm"`
	NATS        natsTOML            `toml:"nats"`
	TaskService taskServiceTOML     `toml:"task_service"`
	OTEL        otelTOML            `toml:"otel"`
	Tools       map[string]toolTOML `toml:"tools,omitempty"`
	Schema      schemaTOML          `toml:"schema"`
	Actions     []actionTOML        `toml:"actions,omitempty"`
}

type kapeTOML struct {
	HandlerName        string `toml:"handler_name"`
	HandlerNamespace   string `toml:"handler_namespace"`
	ClusterName        string `toml:"cluster_name"`
	DryRun             bool   `toml:"dry_run"`
	MaxIterations      int32  `toml:"max_iterations"`
	SchemaName         string `toml:"schema_name"`
	ReplayOnStartup    bool   `toml:"replay_on_startup"`
	MaxEventAgeSeconds int64  `toml:"max_event_age_seconds"`
}

type llmTOML struct {
	Provider     string `toml:"provider"`
	Model        string `toml:"model"`
	SystemPrompt string `toml:"system_prompt"`
}

type natsTOML struct {
	Subject  string `toml:"subject"`
	Consumer string `toml:"consumer"`
	Stream   string `toml:"stream"`
}

type taskServiceTOML struct {
	Endpoint string `toml:"endpoint"`
}

type otelTOML struct {
	Endpoint    string `toml:"endpoint"`
	ServiceName string `toml:"service_name"`
}

type toolTOML struct {
	Type           string `toml:"type"`
	SidecarPort    int    `toml:"sidecar_port,omitempty"`
	Transport      string `toml:"transport,omitempty"`
	QdrantEndpoint string `toml:"qdrant_endpoint,omitempty"`
}

type schemaTOML struct {
	JSON string `toml:"json"`
}

type actionTOML struct {
	Name      string                 `toml:"name"`
	Condition string                 `toml:"condition"`
	Type      string                 `toml:"type"`
	Data      map[string]interface{} `toml:"data,omitempty"`
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
cd operator && go test ./infra/toml/... -v
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
cd operator && git add infra/toml/renderer.go infra/toml/renderer_test.go
git commit -m "feat(operator): TOML renderer adds tool sections and schema section"
```

---

## Task 9: DeploymentAdapter — sidecar injection

**Files:**
- Modify: `operator/infra/k8s/deployment.go`
- Create: `operator/infra/k8s/deployment_test.go`

- [ ] **Step 1: Write the failing tests**

Create `operator/infra/k8s/deployment_test.go`:

```go
package k8s_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
)

func newDeploymentScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func TestDeploymentAdapter_InjectsSidecarForMCPTool(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "test-handler", Namespace: "kape-system", UID: "uid-h"},
		Spec: v1alpha1.KapeHandlerSpec{
			Tools: []v1alpha1.ToolRef{{Ref: "grafana-mcp"}},
		},
	}
	auditEnabled := true
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "grafana-mcp"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "mcp",
			MCP: &v1alpha1.MCPSpec{
				Upstream:     v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://grafana:8080"},
				AllowedTools: []string{"grafana_query"},
				Audit:        &v1alpha1.AuditSpec{Enabled: &auditEnabled},
			},
		},
	}}

	c := fake.NewClientBuilder().WithScheme(newDeploymentScheme()).Build()
	adapter := k8sadapters.NewDeploymentAdapter(c)
	cfg := domainconfig.KapeConfig{}

	err := adapter.Ensure(context.Background(), handler, cfg, "hash-abc", tools)
	require.NoError(t, err)

	var dep appsv1.Deployment
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-test-handler", Namespace: "kape-system"}, &dep)
	require.NoError(t, err)

	// handler container + 1 sidecar
	assert.Len(t, dep.Spec.Template.Spec.Containers, 2)

	sidecar := dep.Spec.Template.Spec.Containers[1]
	assert.Equal(t, "kapetool-grafana-mcp", sidecar.Name)

	envMap := make(map[string]string)
	for _, e := range sidecar.Env {
		envMap[e.Name] = e.Value
	}
	assert.Equal(t, "http://grafana:8080", envMap["KAPETOOL_UPSTREAM_URL"])
	assert.Equal(t, "sse", envMap["KAPETOOL_UPSTREAM_TRANSPORT"])

	var allowedTools []string
	_ = json.Unmarshal([]byte(envMap["KAPETOOL_ALLOWED_TOOLS"]), &allowedTools)
	assert.Equal(t, []string{"grafana_query"}, allowedTools)
}

func TestDeploymentAdapter_NoSidecarForMemoryTool(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "test-handler", Namespace: "kape-system", UID: "uid-h"},
		Spec: v1alpha1.KapeHandlerSpec{
			Tools: []v1alpha1.ToolRef{{Ref: "my-memory"}},
		},
	}
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "my-memory"},
		Spec:       v1alpha1.KapeToolSpec{Type: "memory"},
	}}

	c := fake.NewClientBuilder().WithScheme(newDeploymentScheme()).Build()
	adapter := k8sadapters.NewDeploymentAdapter(c)

	err := adapter.Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "hash-123", tools)
	require.NoError(t, err)

	var dep appsv1.Deployment
	_ = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-test-handler", Namespace: "kape-system"}, &dep)

	// handler container only — no sidecar for memory type
	assert.Len(t, dep.Spec.Template.Spec.Containers, 1)
}
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
cd operator && go test ./infra/k8s/... -run TestDeploymentAdapter -v 2>&1 | head -20
```

Expected: compile error — `Ensure` takes 4 args but now called with 5 (tools).

- [ ] **Step 3: Update DeploymentAdapter to inject sidecars**

Replace the full contents of `operator/infra/k8s/deployment.go` with:

```go
package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// DeploymentAdapter implements ports.DeploymentPort.
type DeploymentAdapter struct {
	client client.Client
}

// NewDeploymentAdapter creates a new DeploymentAdapter.
func NewDeploymentAdapter(c client.Client) *DeploymentAdapter {
	return &DeploymentAdapter{client: c}
}

func deploymentName(handlerName string) string { return "kape-handler-" + handlerName }

// Ensure creates or patches the handler Deployment with sidecar injection for mcp-type tools.
func (a *DeploymentAdapter) Ensure(
	ctx context.Context,
	handler *v1alpha1.KapeHandler,
	cfg domainconfig.KapeConfig,
	rolloutHash string,
	tools []v1alpha1.KapeTool,
) error {
	name := deploymentName(handler.Name)
	key := types.NamespacedName{Name: name, Namespace: handler.Namespace}
	desired := buildDeployment(handler, cfg, rolloutHash, tools)

	var existing appsv1.Deployment
	err := a.client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		return a.client.Create(ctx, &desired)
	}
	if err != nil {
		return fmt.Errorf("getting Deployment %s/%s: %w", handler.Namespace, name, err)
	}
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Spec = desired.Spec
	existing.Annotations = desired.Annotations
	existing.Labels = desired.Labels
	return a.client.Patch(ctx, &existing, patch)
}

// GetStatus reads the Deployment status. found is false when the Deployment does not exist.
func (a *DeploymentAdapter) GetStatus(ctx context.Context, key types.NamespacedName) (*appsv1.DeploymentStatus, bool, error) {
	var dep appsv1.Deployment
	if err := a.client.Get(ctx, key, &dep); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("getting Deployment %s: %w", key, err)
	}
	return &dep.Status, true, nil
}

func buildDeployment(handler *v1alpha1.KapeHandler, cfg domainconfig.KapeConfig, rolloutHash string, tools []v1alpha1.KapeTool) appsv1.Deployment {
	cfg = cfg.WithDefaults()
	name := deploymentName(handler.Name)
	saName := serviceAccountName(handler.Name)
	cmName := configMapName(handler.Name)
	noAutoMount := false

	var replicas int32 = 1
	if handler.Spec.Scaling != nil && handler.Spec.Scaling.MinReplicas > 0 {
		replicas = handler.Spec.Scaling.MinReplicas
	}

	envVars := []corev1.EnvVar{
		{Name: "KAPE_HANDLER_NAME", Value: handler.Name},
		{Name: "KAPE_NAMESPACE", Value: handler.Namespace},
	}
	envVars = append(envVars, handler.Spec.Envs...)

	handlerContainer := corev1.Container{
		Name:  "handler",
		Image: cfg.HandlerImageRef(),
		Env:   envVars,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "settings",
			MountPath: "/etc/kape",
			ReadOnly:  true,
		}},
	}

	containers := append([]corev1.Container{handlerContainer}, buildSidecars(handler, tools, cfg)...)

	dep := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: handler.Namespace,
			Labels: map[string]string{
				"kape.io/handler":              handler.Name,
				"app.kubernetes.io/managed-by": "kape-operator",
				"app.kubernetes.io/name":       name,
			},
			Annotations: map[string]string{"kape.io/rollout-hash": rolloutHash},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kape.io/handler": handler.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"kape.io/handler":        handler.Name,
						"app.kubernetes.io/name": name,
					},
					Annotations: map[string]string{"kape.io/rollout-hash": rolloutHash},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           saName,
					AutomountServiceAccountToken: &noAutoMount,
					Containers:                   containers,
					Volumes: []corev1.Volume{{
						Name: "settings",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
							},
						},
					}},
				},
			},
		},
	}
	setOwnerRef(handler, &dep.ObjectMeta)
	return dep
}

func buildSidecars(handler *v1alpha1.KapeHandler, tools []v1alpha1.KapeTool, cfg domainconfig.KapeConfig) []corev1.Container {
	toolMap := make(map[string]v1alpha1.KapeTool, len(tools))
	for _, t := range tools {
		toolMap[t.Name] = t
	}

	var sidecars []corev1.Container
	sidecarPort := int32(8080)
	taskServiceEndpoint := fmt.Sprintf("http://kape-task-service.%s:8080", handler.Namespace)

	for _, ref := range handler.Spec.Tools {
		t, ok := toolMap[ref.Ref]
		if !ok || t.Spec.Type != "mcp" {
			continue
		}
		mcp := t.Spec.MCP

		auditEnabled := "true"
		if mcp.Audit != nil && mcp.Audit.Enabled != nil && !*mcp.Audit.Enabled {
			auditEnabled = "false"
		}

		allowedToolsJSON := "[]"
		if len(mcp.AllowedTools) > 0 {
			if b, err := json.Marshal(mcp.AllowedTools); err == nil {
				allowedToolsJSON = string(b)
			}
		}

		redactionInput, redactionOutput := "[]", "[]"
		if mcp.Redaction != nil {
			if b, err := json.Marshal(mcp.Redaction.Input); err == nil {
				redactionInput = string(b)
			}
			if b, err := json.Marshal(mcp.Redaction.Output); err == nil {
				redactionOutput = string(b)
			}
		}

		sidecars = append(sidecars, corev1.Container{
			Name:  "kapetool-" + ref.Ref,
			Image: cfg.KapetoolImageRef(),
			Ports: []corev1.ContainerPort{{
				Name:          "mcp",
				ContainerPort: sidecarPort,
				Protocol:      corev1.ProtocolTCP,
			}},
			Env: []corev1.EnvVar{
				{Name: "KAPETOOL_UPSTREAM_URL", Value: mcp.Upstream.URL},
				{Name: "KAPETOOL_UPSTREAM_TRANSPORT", Value: mcp.Upstream.Transport},
				{Name: "KAPETOOL_ALLOWED_TOOLS", Value: allowedToolsJSON},
				{Name: "KAPETOOL_REDACTION_INPUT", Value: redactionInput},
				{Name: "KAPETOOL_REDACTION_OUTPUT", Value: redactionOutput},
				{Name: "KAPETOOL_AUDIT_ENABLED", Value: auditEnabled},
				{Name: "KAPETOOL_TASK_SERVICE_ENDPOINT", Value: taskServiceEndpoint},
				{Name: "KAPETOOL_LISTEN_PORT", Value: fmt.Sprintf("%d", sidecarPort)},
			},
		})
		sidecarPort++
	}
	return sidecars
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
cd operator && go test ./infra/k8s/... -run TestDeploymentAdapter -v
```

Expected: both tests PASS.

- [ ] **Step 5: Run all infra tests**

```bash
cd operator && go test ./infra/... -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
cd operator && git add infra/k8s/deployment.go infra/k8s/deployment_test.go
git commit -m "feat(operator): DeploymentAdapter injects kapetool sidecars for mcp-type tools"
```

---

## Plan A Complete

All infrastructure building blocks are in place. Plan B (reconcilers) can now be executed.

**Verify the full operator still builds (even with broken reconciler from port signature change):**

```bash
cd operator && go build ./... 2>&1 | grep -v "controller/reconcile/handler.go" | head -20
```

The only expected compile errors are in `controller/reconcile/handler.go` (Phase 2 reconciler using old `Render`/`Ensure` signatures) and `cmd/main.go`. These are fixed in Plan B Task 1.
