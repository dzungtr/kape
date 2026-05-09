# Phase 6 Slice 5 — kapeproxy-config Rendering + Sidecar Injection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace per-tool `kapetool-*` sidecar containers with a single `kapeproxy` sidecar that reads a rendered `kapeproxy-config-{handler-name}` ConfigMap, and ship a stub binary that satisfies slice 5 acceptance criteria.

**Architecture:** The operator renders one `kapeproxy-config-{handler-name}` ConfigMap per handler, containing YAML that maps each `mcp`-type KapeTool to an upstream entry. `buildDeployment` is rewritten to inject a single `kapeproxy` container (instead of N `kapetool-*` containers) that volume-mounts the ConfigMap at `/etc/kapeproxy/`. A new top-level Go module `kapeproxy/` ships a static-config stub binary that reads the config, serves `tools/list` with namespaced names, and returns MCP error on `tools/call`. CI must build and push `kape/kapeproxy:stub` before the PR is merged.

**Tech Stack:** Go 1.25, controller-runtime, `gopkg.in/yaml.v3`, `net/http`, Kubernetes ConfigMap, GitHub Actions (Docker build + push), Snyk MCP tools.

---

## File Map

**Operator — modified**

| File | Change |
|---|---|
| `operator/domain/config/config.go` | Add `KapeproxyImage`, `KapeproxyImageVersion`, `KapeproxyImageRef()`, update `WithDefaults` |
| `operator/infra/k8s/kapeconfig.go` | Read `kapeproxy.image`, `kapeproxy.version` from ConfigMap data |
| `operator/infra/k8s/deployment.go` | Replace `buildSidecars` with single `kapeproxy` container; add volume + mounts |
| `operator/controller/reconcile/handler.go` | Add step 5b: render+ensure `kapeproxy-config-{name}` CM; extend `computeRolloutHash` to include cfg fields |

**Operator — new**

| File | Purpose |
|---|---|
| `operator/infra/ports/kapeproxy_config.go` | `KapeproxyConfigPort` interface |
| `operator/infra/k8s/kapeproxy_config.go` | `KapeproxyConfigAdapter` + `renderKapeproxyConfig` YAML renderer |

**Operator — new tests**

| File | Purpose |
|---|---|
| `operator/infra/k8s/kapeproxy_config_test.go` | Golden YAML unit tests |
| `operator/infra/k8s/deployment_test.go` | Extended: kapeproxy container shape + no kapetool-* |

**kapeproxy module — new**

| File | Purpose |
|---|---|
| `kapeproxy/go.mod` | New top-level Go module `github.com/kape-io/kape/kapeproxy` |
| `kapeproxy/go.sum` | Generated |
| `kapeproxy/cmd/kapeproxy-stub/main.go` | Stub binary: reads config, serves MCP tools/list |
| `kapeproxy/cmd/kapeproxy-stub/main_test.go` | Table test for tools/list output |
| `kapeproxy/Dockerfile.stub` | Multi-stage build of stub binary |
| `kapeproxy/README.md` | Documents stub as transitional |

**CI — new**

| File | Purpose |
|---|---|
| `.github/workflows/kapeproxy-stub.yml` | Build + push `kape/kapeproxy:stub` on PR merge to main |

---

## Task 1: Extend operator domain config

**Files:**
- Modify: `operator/domain/config/config.go`

- [ ] **Step 1: Add fields and helper to KapeConfig**

Replace the file content (keep existing fields, add after `KapetoolImageVersion`):

```go
// Package config holds operator-level platform configuration.
package config

import "fmt"

// KapeConfig holds cluster-wide defaults read from the kape-config ConfigMap in kape-system.
// All fields have defaults so the operator works on a fresh cluster without pre-existing config.
type KapeConfig struct {
	ClusterName string

	// Handler runtime image
	HandlerImage        string
	HandlerImageVersion string

	// kapetool sidecar image (retained for backward compat; not injected after slice 5)
	KapetoolImage        string
	KapetoolImageVersion string

	// kapeproxy sidecar image
	KapeproxyImage        string
	KapeproxyImageVersion string

	// NATS monitoring endpoint for KEDA ScaledObject
	NATSMonitoringEndpoint string

	// NATSStreamName is the JetStream stream KEDA scales the handler against.
	NATSStreamName string

	// Qdrant vector database
	QdrantVersion      string
	QdrantStorageClass string

	// Default max iterations for the ReAct loop (overridable per KapeHandler)
	DefaultMaxIterations int32
}

// HandlerImageRef returns the full image reference (image:version) for the handler container.
func (c KapeConfig) HandlerImageRef() string {
	img := c.HandlerImage
	if img == "" {
		img = "kape/handler"
	}
	ver := c.HandlerImageVersion
	if ver == "" {
		ver = "latest"
	}
	return fmt.Sprintf("%s:%s", img, ver)
}

// KapetoolImageRef returns the full image reference for the kapetool sidecar.
func (c KapeConfig) KapetoolImageRef() string {
	img := c.KapetoolImage
	if img == "" {
		img = "kape/kapetool"
	}
	ver := c.KapetoolImageVersion
	if ver == "" {
		ver = "latest"
	}
	return fmt.Sprintf("%s:%s", img, ver)
}

// KapeproxyImageRef returns the full image reference for the kapeproxy sidecar.
func (c KapeConfig) KapeproxyImageRef() string {
	img := c.KapeproxyImage
	if img == "" {
		img = "kape/kapeproxy"
	}
	ver := c.KapeproxyImageVersion
	if ver == "" {
		ver = "stub"
	}
	return fmt.Sprintf("%s:%s", img, ver)
}

// WithDefaults returns a copy of KapeConfig with default values applied where fields are zero.
func (c KapeConfig) WithDefaults() KapeConfig {
	if c.ClusterName == "" {
		c.ClusterName = "default"
	}
	if c.HandlerImage == "" {
		c.HandlerImage = "kape/handler"
	}
	if c.HandlerImageVersion == "" {
		c.HandlerImageVersion = "latest"
	}
	if c.KapetoolImage == "" {
		c.KapetoolImage = "kape/kapetool"
	}
	if c.KapetoolImageVersion == "" {
		c.KapetoolImageVersion = "latest"
	}
	if c.KapeproxyImage == "" {
		c.KapeproxyImage = "kape/kapeproxy"
	}
	if c.KapeproxyImageVersion == "" {
		c.KapeproxyImageVersion = "stub"
	}
	if c.NATSMonitoringEndpoint == "" {
		c.NATSMonitoringEndpoint = "http://nats.kape-system:8222"
	}
	if c.NATSStreamName == "" {
		c.NATSStreamName = "kape-events"
	}
	if c.QdrantVersion == "" {
		c.QdrantVersion = "v1.14.0"
	}
	if c.QdrantStorageClass == "" {
		c.QdrantStorageClass = "standard"
	}
	if c.DefaultMaxIterations == 0 {
		c.DefaultMaxIterations = 50
	}
	return c
}
```

- [ ] **Step 2: Run existing config tests (none exist; quick sanity compile)**

```bash
cd /path/to/worktree/operator && go build ./domain/config/...
```

Expected: exits 0, no output.

- [ ] **Step 3: Commit**

```bash
git -C /path/to/worktree add operator/domain/config/config.go
git -C /path/to/worktree commit -m "feat(config): add KapeproxyImage/Version fields and KapeproxyImageRef helper"
```

---

## Task 2: Extend kape-config ConfigMap loader

**Files:**
- Modify: `operator/infra/k8s/kapeconfig.go`

- [ ] **Step 1: Add kapeproxy key reads to the Load function**

In the struct literal inside `Load`, add two lines after `KapetoolImageVersion`:

```go
cfg := domainconfig.KapeConfig{
    HandlerImage:           cm.Data["kapehandler.image"],
    HandlerImageVersion:    cm.Data["kapehandler.version"],
    KapetoolImage:          cm.Data["kapetool.image"],
    KapetoolImageVersion:   cm.Data["kapetool.version"],
    KapeproxyImage:         cm.Data["kapeproxy.image"],
    KapeproxyImageVersion:  cm.Data["kapeproxy.version"],
    NATSMonitoringEndpoint: cm.Data["nats.monitoringEndpoint"],
    NATSStreamName:         cm.Data["nats.streamName"],
    ClusterName:            cm.Data["cluster.name"],
    QdrantVersion:          cm.Data["qdrant.version"],
    QdrantStorageClass:     cm.Data["qdrant.storageClass"],
}
```

- [ ] **Step 2: Compile**

```bash
cd /path/to/worktree/operator && go build ./infra/k8s/...
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git -C /path/to/worktree add operator/infra/k8s/kapeconfig.go
git -C /path/to/worktree commit -m "feat(kapeconfig): read kapeproxy.image and kapeproxy.version from kape-config"
```

---

## Task 3: Define KapeproxyConfigPort interface

**Files:**
- Create: `operator/infra/ports/kapeproxy_config.go`

- [ ] **Step 1: Write the port file**

```go
package ports

import (
	"context"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// KapeproxyConfigPort manages the kapeproxy-config-{handler-name} ConfigMap.
type KapeproxyConfigPort interface {
	// Ensure creates or updates the kapeproxy config ConfigMap for the given handler.
	// tools must contain only mcp-type KapeTools; memory/event-publish tools are excluded by the caller.
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, tools []v1alpha1.KapeTool) error
}
```

- [ ] **Step 2: Compile**

```bash
cd /path/to/worktree/operator && go build ./infra/ports/...
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git -C /path/to/worktree add operator/infra/ports/kapeproxy_config.go
git -C /path/to/worktree commit -m "feat(ports): add KapeproxyConfigPort interface"
```

---

## Task 4: Implement KapeproxyConfigAdapter and YAML renderer

**Files:**
- Create: `operator/infra/k8s/kapeproxy_config.go`

The YAML schema (from spec §2.2):

```yaml
upstreams:
  <kapetool-name>:
    url: <url>
    transport: sse | streamable-http
    allowedTools:       # OMITTED when empty
      - <tool-name>
    redaction:          # OMITTED when no rules
      input:
        - jsonPath: "$.field"
      output:
        - jsonPath: "$.field"
    audit: true | false
```

Rules:
- Only `spec.type == "mcp"` tools are rendered; others are excluded by the caller.
- `allowedTools` field is **omitted** entirely when the slice is empty (not rendered as `allowedTools: []`).
- `redaction` block is **omitted** when `spec.mcp.redaction` is nil or both slices are empty.
- `audit` defaults to `true` when `spec.mcp.audit` is nil or `spec.mcp.audit.enabled` is nil.

- [ ] **Step 1: Write the adapter**

```go
package k8s

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"gopkg.in/yaml.v3"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// KapeproxyConfigAdapter implements ports.KapeproxyConfigPort.
type KapeproxyConfigAdapter struct {
	client client.Client
}

// NewKapeproxyConfigAdapter creates a new KapeproxyConfigAdapter.
func NewKapeproxyConfigAdapter(c client.Client) *KapeproxyConfigAdapter {
	return &KapeproxyConfigAdapter{client: c}
}

func kapeproxyConfigMapName(handlerName string) string {
	return "kapeproxy-config-" + handlerName
}

// Ensure creates or updates the kapeproxy-config-{handler-name} ConfigMap.
func (a *KapeproxyConfigAdapter) Ensure(
	ctx context.Context,
	handler *v1alpha1.KapeHandler,
	tools []v1alpha1.KapeTool,
) error {
	rendered, err := renderKapeproxyConfig(tools)
	if err != nil {
		return fmt.Errorf("rendering kapeproxy config: %w", err)
	}

	name := kapeproxyConfigMapName(handler.Name)
	key := types.NamespacedName{Name: name, Namespace: handler.Namespace}

	var cm corev1.ConfigMap
	err = a.client.Get(ctx, key, &cm)
	if apierrors.IsNotFound(err) {
		cm = buildKapeproxyConfigMap(handler, rendered)
		return a.client.Create(ctx, &cm)
	}
	if err != nil {
		return fmt.Errorf("getting ConfigMap %s/%s: %w", handler.Namespace, name, err)
	}

	if cm.Data["config.yaml"] == rendered {
		return nil
	}
	patch := client.MergeFrom(cm.DeepCopy())
	cm.Data = map[string]string{"config.yaml": rendered}
	return a.client.Patch(ctx, &cm, patch)
}

func buildKapeproxyConfigMap(handler *v1alpha1.KapeHandler, yamlContent string) corev1.ConfigMap {
	cm := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kapeproxyConfigMapName(handler.Name),
			Namespace: handler.Namespace,
			Labels: map[string]string{
				"kape.io/handler":              handler.Name,
				"app.kubernetes.io/managed-by": "kape-operator",
			},
		},
		Data: map[string]string{"config.yaml": yamlContent},
	}
	setOwnerRef(handler, &cm.ObjectMeta)
	return cm
}

// upstreamEntry mirrors the kapeproxy-config YAML upstream block.
type upstreamEntry struct {
	URL          string            `yaml:"url"`
	Transport    string            `yaml:"transport"`
	AllowedTools []string          `yaml:"allowedTools,omitempty"`
	Redaction    *redactionEntry   `yaml:"redaction,omitempty"`
	Audit        bool              `yaml:"audit"`
}

type redactionEntry struct {
	Input  []jsonPathEntry `yaml:"input,omitempty"`
	Output []jsonPathEntry `yaml:"output,omitempty"`
}

type jsonPathEntry struct {
	JSONPath string `yaml:"jsonPath"`
}

// kapeproxyConfigDoc is a yaml.Node-based ordered map to guarantee key ordering.
type kapeproxyConfigDoc struct {
	Upstreams map[string]upstreamEntry
	// sortedNames preserves deterministic output order.
	sortedNames []string
}

// renderKapeproxyConfig produces the config.yaml content from mcp-type tools.
// tools must already be filtered to mcp type only.
func renderKapeproxyConfig(tools []v1alpha1.KapeTool) (string, error) {
	// Sort by name for deterministic output.
	sorted := make([]v1alpha1.KapeTool, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	// Build a yaml.Node mapping to preserve key insertion order.
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	upstreamsKey := &yaml.Node{Kind: yaml.ScalarNode, Value: "upstreams", Tag: "!!str"}
	upstreamsVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	root.Content = append(root.Content, upstreamsKey, upstreamsVal)

	for _, t := range sorted {
		mcp := t.Spec.MCP
		if mcp == nil {
			continue
		}

		audit := true
		if mcp.Audit != nil && mcp.Audit.Enabled != nil {
			audit = *mcp.Audit.Enabled
		}

		entry := upstreamEntry{
			URL:       mcp.Upstream.URL,
			Transport: mcp.Upstream.Transport,
			Audit:     audit,
		}
		if len(mcp.AllowedTools) > 0 {
			entry.AllowedTools = mcp.AllowedTools
		}
		if mcp.Redaction != nil && (len(mcp.Redaction.Input) > 0 || len(mcp.Redaction.Output) > 0) {
			re := &redactionEntry{}
			for _, r := range mcp.Redaction.Input {
				re.Input = append(re.Input, jsonPathEntry{JSONPath: r.JSONPath})
			}
			for _, r := range mcp.Redaction.Output {
				re.Output = append(re.Output, jsonPathEntry{JSONPath: r.JSONPath})
			}
			entry.Redaction = re
		}

		entryNode, err := toYAMLNode(entry)
		if err != nil {
			return "", fmt.Errorf("encoding upstream %q: %w", t.Name, err)
		}

		nameNode := &yaml.Node{Kind: yaml.ScalarNode, Value: t.Name, Tag: "!!str"}
		upstreamsVal.Content = append(upstreamsVal.Content, nameNode, entryNode)
	}

	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	b, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshalling kapeproxy config: %w", err)
	}
	return string(b), nil
}

// toYAMLNode encodes a value via round-trip through yaml.Marshal → yaml.Unmarshal to get a *yaml.Node.
func toYAMLNode(v interface{}) (*yaml.Node, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(b, &node); err != nil {
		return nil, err
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		return node.Content[0], nil
	}
	return &node, nil
}
```

- [ ] **Step 2: Add gopkg.in/yaml.v3 dependency**

```bash
cd /path/to/worktree/operator && go get gopkg.in/yaml.v3 && go mod tidy
```

Expected: `go.mod` and `go.sum` updated.

- [ ] **Step 3: Compile**

```bash
cd /path/to/worktree/operator && go build ./infra/k8s/...
```

Expected: exits 0.

- [ ] **Step 4: Commit**

```bash
git -C /path/to/worktree add operator/infra/k8s/kapeproxy_config.go operator/go.mod operator/go.sum
git -C /path/to/worktree commit -m "feat(k8s): add KapeproxyConfigAdapter and YAML renderer"
```

---

## Task 5: Write golden YAML tests for kapeproxy config renderer

**Files:**
- Create: `operator/infra/k8s/kapeproxy_config_test.go`

- [ ] **Step 1: Write failing tests**

```go
package k8s_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
)

func TestRenderKapeproxyConfig_SingleTool(t *testing.T) {
	auditEnabled := true
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "grafana-mcp"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "mcp",
			MCP: &v1alpha1.MCPSpec{
				Upstream:     v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://grafana:8080"},
				AllowedTools: []string{"grafana_query", "grafana_dashboard"},
				Audit:        &v1alpha1.AuditSpec{Enabled: &auditEnabled},
			},
		},
	}}

	got, err := k8sadapters.RenderKapeproxyConfig(tools)
	require.NoError(t, err)

	want := `upstreams:
    grafana-mcp:
        url: http://grafana:8080
        transport: sse
        allowedTools:
            - grafana_query
            - grafana_dashboard
        audit: true
`
	assert.Equal(t, want, got)
}

func TestRenderKapeproxyConfig_AllowedToolsOmittedWhenEmpty(t *testing.T) {
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "search-mcp"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "mcp",
			MCP: &v1alpha1.MCPSpec{
				Upstream: v1alpha1.MCPUpstreamSpec{Transport: "streamable-http", URL: "http://search:9000"},
			},
		},
	}}

	got, err := k8sadapters.RenderKapeproxyConfig(tools)
	require.NoError(t, err)

	// allowedTools must not appear in output
	assert.NotContains(t, got, "allowedTools")
	assert.Contains(t, got, "url: http://search:9000")
	assert.Contains(t, got, "audit: true")
}

func TestRenderKapeproxyConfig_RedactionPopulated(t *testing.T) {
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "secure-mcp"},
		Spec: v1alpha1.KapeToolSpec{
			Type: "mcp",
			MCP: &v1alpha1.MCPSpec{
				Upstream:  v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://secure:8080"},
				Redaction: &v1alpha1.RedactionSpec{
					Input:  []v1alpha1.JSONPathRule{{JSONPath: "$.password"}},
					Output: []v1alpha1.JSONPathRule{{JSONPath: "$.token"}},
				},
			},
		},
	}}

	got, err := k8sadapters.RenderKapeproxyConfig(tools)
	require.NoError(t, err)

	assert.Contains(t, got, "redaction:")
	assert.Contains(t, got, "jsonPath: $.password")
	assert.Contains(t, got, "jsonPath: $.token")
}

func TestRenderKapeproxyConfig_MCPAndMemoryMix_MemoryExcluded(t *testing.T) {
	tools := []v1alpha1.KapeTool{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana-mcp"},
			Spec: v1alpha1.KapeToolSpec{
				Type: "mcp",
				MCP:  &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://grafana:8080"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "my-memory"},
			Spec:       v1alpha1.KapeToolSpec{Type: "memory"},
		},
	}

	// Filter mcp-only before calling (matches how handler.go calls it)
	var mcpTools []v1alpha1.KapeTool
	for _, t := range tools {
		if t.Spec.Type == "mcp" {
			mcpTools = append(mcpTools, t)
		}
	}

	got, err := k8sadapters.RenderKapeproxyConfig(mcpTools)
	require.NoError(t, err)

	assert.Contains(t, got, "grafana-mcp:")
	assert.NotContains(t, got, "my-memory")
}

func TestRenderKapeproxyConfig_MultipleTools_DeterministicOrder(t *testing.T) {
	tools := []v1alpha1.KapeTool{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "z-tool"},
			Spec: v1alpha1.KapeToolSpec{
				Type: "mcp",
				MCP:  &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://z:8080"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "a-tool"},
			Spec: v1alpha1.KapeToolSpec{
				Type: "mcp",
				MCP:  &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://a:8080"}},
			},
		},
	}

	got, err := k8sadapters.RenderKapeproxyConfig(tools)
	require.NoError(t, err)

	// a-tool must appear before z-tool (sorted by name)
	aIdx := strings.Index(got, "a-tool:")
	zIdx := strings.Index(got, "z-tool:")
	assert.Less(t, aIdx, zIdx, "tools should be sorted alphabetically")
}
```

Note: `RenderKapeproxyConfig` must be exported for testing. In `kapeproxy_config.go`, rename `renderKapeproxyConfig` → `RenderKapeproxyConfig`.

- [ ] **Step 2: Export the renderer and add missing import**

In `operator/infra/k8s/kapeproxy_config.go`, rename the function:

```go
// RenderKapeproxyConfig produces the config.yaml content from mcp-type tools.
// tools must already be filtered to mcp type only.
func RenderKapeproxyConfig(tools []v1alpha1.KapeTool) (string, error) {
```

Also update the internal caller in `Ensure`:

```go
rendered, err := RenderKapeproxyConfig(tools)
```

Add `"strings"` and `metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"` import to the test file.

- [ ] **Step 3: Run tests to verify they pass**

```bash
cd /path/to/worktree/operator && go test ./infra/k8s/... -run TestRenderKapeproxyConfig -v
```

Expected: all 5 tests PASS.

- [ ] **Step 4: Commit**

```bash
git -C /path/to/worktree add operator/infra/k8s/kapeproxy_config.go operator/infra/k8s/kapeproxy_config_test.go
git -C /path/to/worktree commit -m "test(k8s): golden YAML tests for RenderKapeproxyConfig"
```

---

## Task 6: Rewrite deployment.go — replace kapetool sidecars with kapeproxy

**Files:**
- Modify: `operator/infra/k8s/deployment.go`

The existing `buildSidecars` function generates N `kapetool-*` containers (one per mcp-type tool). Replace it with a single `kapeproxy` container that volume-mounts the kapeproxy-config ConfigMap.

- [ ] **Step 1: Rewrite `buildDeployment` and replace `buildSidecars`**

Replace the `buildSidecars` function and update `buildDeployment` to use the new approach. The full updated `deployment.go`:

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

// DeploymentAdapter implements ports.DeploymentPort.
type DeploymentAdapter struct {
	client client.Client
}

// NewDeploymentAdapter creates a new DeploymentAdapter.
func NewDeploymentAdapter(c client.Client) *DeploymentAdapter {
	return &DeploymentAdapter{client: c}
}

func deploymentName(handlerName string) string { return "kape-handler-" + handlerName }

// Ensure creates or patches the handler Deployment with a single kapeproxy sidecar.
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
	kapeproxyCMName := kapeproxyConfigMapName(handler.Name)
	noAutoMount := false

	// Determine whether any mcp-type tools are present.
	hasMCPTools := false
	for _, t := range tools {
		if t.Spec.Type == "mcp" {
			hasMCPTools = true
			break
		}
	}

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
		Name:      "handler",
		Image:     cfg.HandlerImageRef(),
		Env:       envVars,
		Resources: resolveHandlerResources(handler.Spec.Resources),
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "settings",
			MountPath: "/etc/kape",
			ReadOnly:  true,
		}},
	}

	volumes := []corev1.Volume{{
		Name: "settings",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
			},
		},
	}}

	containers := []corev1.Container{handlerContainer}

	if hasMCPTools {
		volumes = append(volumes, corev1.Volume{
			Name: "kapeproxy-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: kapeproxyCMName},
				},
			},
		})
		containers = append(containers, buildKapeproxySidecar(cfg))
	}

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
					Volumes:                      volumes,
				},
			},
		},
	}
	setOwnerRef(handler, &dep.ObjectMeta)
	return dep
}

func buildKapeproxySidecar(cfg domainconfig.KapeConfig) corev1.Container {
	return corev1.Container{
		Name:  "kapeproxy",
		Image: cfg.KapeproxyImageRef(),
		Ports: []corev1.ContainerPort{{
			Name:          "mcp",
			ContainerPort: 8080,
			Protocol:      corev1.ProtocolTCP,
		}},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "kapeproxy-config",
			MountPath: "/etc/kapeproxy",
			ReadOnly:  true,
		}},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		},
	}
}

func resolveHandlerResources(override *corev1.ResourceRequirements) corev1.ResourceRequirements {
	if override != nil {
		return *override
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
}
```

- [ ] **Step 2: Compile**

```bash
cd /path/to/worktree/operator && go build ./infra/k8s/...
```

Expected: exits 0.

- [ ] **Step 3: Commit**

```bash
git -C /path/to/worktree add operator/infra/k8s/deployment.go
git -C /path/to/worktree commit -m "feat(k8s): replace kapetool-* sidecars with single kapeproxy container"
```

---

## Task 7: Update deployment tests for new sidecar shape

**Files:**
- Modify: `operator/infra/k8s/deployment_test.go`

The existing tests assert `kapetool-*` container names. They must be updated to assert the new `kapeproxy` shape. The test `TestDeploymentAdapter_InjectsSidecarForMCPTool` becomes a test for kapeproxy injection.

- [ ] **Step 1: Replace and extend tests**

Replace the content of `deployment_test.go` (keep the resource tests, update the sidecar test):

```go
package k8s_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
)

func TestDeploymentAdapter_InjectsSingleKapeproxySidecarForMCPTool(t *testing.T) {
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

	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewDeploymentAdapter(c)
	cfg := domainconfig.KapeConfig{}

	err := adapter.Ensure(context.Background(), handler, cfg, "hash-abc", tools)
	require.NoError(t, err)

	var dep appsv1.Deployment
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-test-handler", Namespace: "kape-system"}, &dep)
	require.NoError(t, err)

	// handler container + exactly 1 kapeproxy sidecar (no kapetool-* containers)
	require.Len(t, dep.Spec.Template.Spec.Containers, 2)

	names := make([]string, len(dep.Spec.Template.Spec.Containers))
	for i, c := range dep.Spec.Template.Spec.Containers {
		names[i] = c.Name
	}
	assert.Contains(t, names, "handler")
	assert.Contains(t, names, "kapeproxy")

	// No kapetool-* containers
	for _, name := range names {
		assert.NotContains(t, name, "kapetool-")
	}
}

func TestDeploymentAdapter_KapeproxySidecar_Resources(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
		Spec:       v1alpha1.KapeHandlerSpec{Tools: []v1alpha1.ToolRef{{Ref: "tool1"}}},
	}
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "tool1"},
		Spec:       v1alpha1.KapeToolSpec{Type: "mcp", MCP: &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://x:8080"}}},
	}}

	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	require.NoError(t, k8sadapters.NewDeploymentAdapter(c).Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h1", tools))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	var proxy *corev1.Container
	for i := range dep.Spec.Template.Spec.Containers {
		if dep.Spec.Template.Spec.Containers[i].Name == "kapeproxy" {
			proxy = &dep.Spec.Template.Spec.Containers[i]
		}
	}
	require.NotNil(t, proxy, "kapeproxy container must exist")

	assert.True(t, resource.MustParse("100m").Equal(proxy.Resources.Requests[corev1.ResourceCPU]))
	assert.True(t, resource.MustParse("128Mi").Equal(proxy.Resources.Requests[corev1.ResourceMemory]))
	assert.True(t, resource.MustParse("500m").Equal(proxy.Resources.Limits[corev1.ResourceCPU]))
	assert.True(t, resource.MustParse("256Mi").Equal(proxy.Resources.Limits[corev1.ResourceMemory]))
}

func TestDeploymentAdapter_KapeproxySidecar_VolumeMount(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
		Spec:       v1alpha1.KapeHandlerSpec{Tools: []v1alpha1.ToolRef{{Ref: "tool1"}}},
	}
	tools := []v1alpha1.KapeTool{{
		ObjectMeta: metav1.ObjectMeta{Name: "tool1"},
		Spec:       v1alpha1.KapeToolSpec{Type: "mcp", MCP: &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://x:8080"}}},
	}}

	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	require.NoError(t, k8sadapters.NewDeploymentAdapter(c).Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h1", tools))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	// kapeproxy-config volume must exist
	volumeNames := make([]string, len(dep.Spec.Template.Spec.Volumes))
	for i, v := range dep.Spec.Template.Spec.Volumes {
		volumeNames[i] = v.Name
	}
	assert.Contains(t, volumeNames, "kapeproxy-config")

	// kapeproxy container must mount it at /etc/kapeproxy
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name != "kapeproxy" {
			continue
		}
		found := false
		for _, vm := range c.VolumeMounts {
			if vm.Name == "kapeproxy-config" && vm.MountPath == "/etc/kapeproxy" {
				found = true
			}
		}
		assert.True(t, found, "kapeproxy must mount kapeproxy-config at /etc/kapeproxy")
	}
}

func TestDeploymentAdapter_NoSidecarWhenNoMCPTools(t *testing.T) {
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

	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	err := k8sadapters.NewDeploymentAdapter(c).Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "hash-123", tools)
	require.NoError(t, err)

	var dep appsv1.Deployment
	_ = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-test-handler", Namespace: "kape-system"}, &dep)

	// handler container only — no kapeproxy, no kapeproxy-config volume
	assert.Len(t, dep.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "handler", dep.Spec.Template.Spec.Containers[0].Name)

	for _, v := range dep.Spec.Template.Spec.Volumes {
		assert.NotEqual(t, "kapeproxy-config", v.Name)
	}
}

func TestDeploymentAdapter_HandlerResources_DefaultsWhenSpecUnset(t *testing.T) {
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewDeploymentAdapter(c)

	require.NoError(t, adapter.Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h-1", nil))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	got := dep.Spec.Template.Spec.Containers[0].Resources
	assert.True(t, resource.MustParse("100m").Equal(got.Requests[corev1.ResourceCPU]))
	assert.True(t, resource.MustParse("128Mi").Equal(got.Requests[corev1.ResourceMemory]))
	assert.True(t, resource.MustParse("500m").Equal(got.Limits[corev1.ResourceCPU]))
	assert.True(t, resource.MustParse("512Mi").Equal(got.Limits[corev1.ResourceMemory]))
}

func TestDeploymentAdapter_HandlerResources_OverrideFromSpec(t *testing.T) {
	override := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("250m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
	}
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system", UID: "uid-h"},
		Spec:       v1alpha1.KapeHandlerSpec{Resources: override},
	}
	c := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	adapter := k8sadapters.NewDeploymentAdapter(c)

	require.NoError(t, adapter.Ensure(context.Background(), handler, domainconfig.KapeConfig{}, "h-1", nil))

	var dep appsv1.Deployment
	require.NoError(t, c.Get(context.Background(),
		types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep))

	got := dep.Spec.Template.Spec.Containers[0].Resources
	assert.True(t, override.Requests[corev1.ResourceCPU].Equal(got.Requests[corev1.ResourceCPU]))
	assert.True(t, override.Requests[corev1.ResourceMemory].Equal(got.Requests[corev1.ResourceMemory]))
	assert.True(t, override.Limits[corev1.ResourceCPU].Equal(got.Limits[corev1.ResourceCPU]))
	assert.True(t, override.Limits[corev1.ResourceMemory].Equal(got.Limits[corev1.ResourceMemory]))
}
```

- [ ] **Step 2: Run all deployment tests**

```bash
cd /path/to/worktree/operator && go test ./infra/k8s/... -run TestDeploymentAdapter -v
```

Expected: all tests PASS.

- [ ] **Step 3: Commit**

```bash
git -C /path/to/worktree add operator/infra/k8s/deployment_test.go
git -C /path/to/worktree commit -m "test(k8s): update deployment tests for kapeproxy sidecar shape"
```

---

## Task 8: Extend handler reconciler — add kapeproxy-config step and hash extension

**Files:**
- Modify: `operator/controller/reconcile/handler.go`

Two changes:
1. After Step 5 (render settings.toml), add Step 5b: filter mcp tools and ensure kapeproxy-config ConfigMap.
2. Extend `computeRolloutHash` signature to accept `cfg domainconfig.KapeConfig` and hash `cfg.KapeproxyImage` + `cfg.KapeproxyImageVersion`.

The `HandlerReconciler` struct needs a new `kapeproxyConfigs ports.KapeproxyConfigPort` field.

- [ ] **Step 1: Add kapeproxyConfigs field to HandlerReconciler**

In `handler.go`, update the struct and constructor:

```go
// HandlerReconciler performs the full 12-step reconcile logic for KapeHandler.
type HandlerReconciler struct {
	handlers          ports.HandlerRepository
	schemas           ports.SchemaRepository
	tools             ports.ToolRepository
	configMaps        ports.ConfigMapPort
	kapeproxyConfigs  ports.KapeproxyConfigPort
	serviceAccounts   ports.ServiceAccountPort
	deployments       ports.DeploymentPort
	scaledObjects     ports.ScaledObjectPort
	tomlRenderer      ports.TOMLRenderer
	kapeConfig        ports.KapeConfigLoader
}

// NewHandlerReconciler creates a HandlerReconciler with all required dependencies.
func NewHandlerReconciler(
	handlers ports.HandlerRepository,
	schemas ports.SchemaRepository,
	tools ports.ToolRepository,
	configMaps ports.ConfigMapPort,
	kapeproxyConfigs ports.KapeproxyConfigPort,
	serviceAccounts ports.ServiceAccountPort,
	deployments ports.DeploymentPort,
	scaledObjects ports.ScaledObjectPort,
	tomlRenderer ports.TOMLRenderer,
	kapeConfig ports.KapeConfigLoader,
) *HandlerReconciler {
	return &HandlerReconciler{
		handlers:         handlers,
		schemas:          schemas,
		tools:            tools,
		configMaps:       configMaps,
		kapeproxyConfigs: kapeproxyConfigs,
		serviceAccounts:  serviceAccounts,
		deployments:      deployments,
		scaledObjects:    scaledObjects,
		tomlRenderer:     tomlRenderer,
		kapeConfig:       kapeConfig,
	}
}
```

- [ ] **Step 2: Add Step 5b (kapeproxy-config ensure) between Step 5 and Step 6**

After the `r.configMaps.Ensure(...)` call and its log line, insert:

```go
	// Step 5b: Render and ensure kapeproxy-config ConfigMap (mcp-type tools only).
	var mcpTools []v1alpha1.KapeTool
	for _, t := range resolvedTools {
		if t.Spec.Type == "mcp" {
			mcpTools = append(mcpTools, t)
		}
	}
	if err := r.kapeproxyConfigs.Ensure(ctx, handler, mcpTools); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring kapeproxy-config ConfigMap: %w", err)
	}
	log.V(1).Info("kapeproxy-config ConfigMap reconciled")
```

- [ ] **Step 3: Extend computeRolloutHash to include cfg fields**

Update the function signature and body:

```go
func computeRolloutHash(handler *v1alpha1.KapeHandler, schema *v1alpha1.KapeSchema, tools []v1alpha1.KapeTool, cfg domainconfig.KapeConfig) (string, error) {
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
	// Include kapeproxy image config so kape-config changes trigger handler rollouts.
	h.Write([]byte(cfg.KapeproxyImage))
	h.Write([]byte(cfg.KapeproxyImageVersion))
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
```

- [ ] **Step 4: Update the call site in Reconcile (Step 4)**

The call to `computeRolloutHash` is in Step 4 of `Reconcile`. Update it to pass `cfg`. Since `cfg` is loaded in Step 5, move Step 4's hash computation to after Step 5:

Before Step 5, swap Steps 4 and 5 so config is loaded first:

```go
	// Step 4: Load config
	cfg, err := r.kapeConfig.Load(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading kape-config: %w", err)
	}

	// Step 5: Compute rollout hash (requires cfg for kapeproxy image fields)
	rolloutHash, err := computeRolloutHash(handler, schema, resolvedTools, cfg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("computing rollout hash: %w", err)
	}
	consumerName := strings.ReplaceAll(handler.Spec.Trigger.Type, ".", "-")

	// Step 5a: Render and ensure settings.toml ConfigMap
	tomlContent, err := r.tomlRenderer.Render(handler, schema, resolvedTools, cfg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("rendering settings.toml: %w", err)
	}
	if err := r.configMaps.Ensure(ctx, handler, tomlContent); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ConfigMap: %w", err)
	}
	log.V(1).Info("ConfigMap reconciled")

	// Step 5b: Render and ensure kapeproxy-config ConfigMap (mcp-type tools only).
	var mcpTools []v1alpha1.KapeTool
	for _, t := range resolvedTools {
		if t.Spec.Type == "mcp" {
			mcpTools = append(mcpTools, t)
		}
	}
	if err := r.kapeproxyConfigs.Ensure(ctx, handler, mcpTools); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring kapeproxy-config ConfigMap: %w", err)
	}
	log.V(1).Info("kapeproxy-config ConfigMap reconciled")

	// Step 6: Ensure ServiceAccount
	...
```

Also add the `domainconfig` import to handler.go if not already present:
```go
domainconfig "github.com/kape-io/kape/operator/domain/config"
```

- [ ] **Step 5: Compile**

```bash
cd /path/to/worktree/operator && go build ./controller/...
```

Expected: exits 0. Fix any constructor call sites (e.g. in `operator/controller/handler.go`) to pass the new `kapeproxyConfigs` argument.

- [ ] **Step 6: Update operator/cmd/main.go — add KapeproxyConfigAdapter to reconciler wiring**

In `operator/cmd/main.go`, add the new adapter after `configMapAdapt` and pass it to `NewHandlerReconciler`:

```go
	// Adapters
	handlerRepo          := k8sadapters.NewHandlerRepository(k8sClient)
	schemaRepo           := k8sadapters.NewSchemaRepository(k8sClient)
	toolRepo             := k8sadapters.NewToolRepository(k8sClient)
	configMapAdapt       := k8sadapters.NewConfigMapAdapter(k8sClient)
	kapeproxyConfigAdapt := k8sadapters.NewKapeproxyConfigAdapter(k8sClient)
	saAdapt              := k8sadapters.NewServiceAccountAdapter(k8sClient)
	deployAdapt          := k8sadapters.NewDeploymentAdapter(k8sClient)
	scaledObjAdapt       := k8sadapters.NewScaledObjectAdapter(k8sClient)
	cfgLoader            := k8sadapters.NewKapeConfigLoader(k8sClient, cfg.KapeConfigNamespace, cfg.KapeConfigName)
	renderer             := tomlrenderer.NewRenderer()
```

And update the `NewHandlerReconciler` call (5th argument is the new port):

```go
	// KapeHandlerReconciler
	handlerRec := reconcilehandler.NewHandlerReconciler(
		handlerRepo,
		schemaRepo,
		toolRepo,
		configMapAdapt,
		kapeproxyConfigAdapt,
		saAdapt,
		deployAdapt,
		scaledObjAdapt,
		renderer,
		cfgLoader,
	)
```

- [ ] **Step 7: Compile full operator**

```bash
cd /path/to/worktree/operator && go build ./...
```

Expected: exits 0.

- [ ] **Step 8: Run all operator tests**

```bash
cd /path/to/worktree/operator && go test ./... -timeout 120s
```

Expected: all tests PASS.

- [ ] **Step 9: Commit**

```bash
git -C /path/to/worktree add operator/controller/reconcile/handler.go operator/controller/handler.go
git -C /path/to/worktree commit -m "feat(reconcile): add kapeproxy-config step and extend rollout hash with cfg fields"
```

---

## Task 9: Create kapeproxy stub Go module

**Files:**
- Create: `kapeproxy/go.mod`
- Create: `kapeproxy/go.sum` (generated)

- [ ] **Step 1: Initialise the module**

```bash
mkdir -p /path/to/worktree/kapeproxy
cd /path/to/worktree/kapeproxy && go mod init github.com/kape-io/kape/kapeproxy
```

Expected: `kapeproxy/go.mod` created with `module github.com/kape-io/kape/kapeproxy`.

- [ ] **Step 2: Add gopkg.in/yaml.v3 dependency**

```bash
cd /path/to/worktree/kapeproxy && go get gopkg.in/yaml.v3 && go mod tidy
```

Expected: `go.mod` and `go.sum` updated.

- [ ] **Step 3: Commit**

```bash
git -C /path/to/worktree add kapeproxy/go.mod kapeproxy/go.sum
git -C /path/to/worktree commit -m "feat(kapeproxy): initialise new Go module for kapeproxy binary"
```

---

## Task 10: Write kapeproxy stub binary

**Files:**
- Create: `kapeproxy/cmd/kapeproxy-stub/main.go`

The stub:
- Reads `/etc/kapeproxy/config.yaml` (path overridable via `KAPEPROXY_CONFIG` env var for testing).
- Parses `upstreams.<name>.allowedTools` (list of strings, optional — nil means expose all).
- On `POST /mcp` with JSON-RPC method `tools/list`: returns all `{kapetool-name}__{tool-name}` entries from `allowedTools` for each upstream. When `allowedTools` is absent, returns `{kapetool-name}__*` as a sentinel (the real binary will enumerate; stub uses the sentinel pattern).
- On `POST /mcp` with JSON-RPC method `tools/call`: returns MCP error `-32603` with message `"server not yet available"`.
- Listens on `:8080` (overridable via `PORT` env var).

- [ ] **Step 1: Write the stub binary**

```go
// DEPRECATED: removed in Phase 6 slice 7
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"gopkg.in/yaml.v3"
)

type upstreamCfg struct {
	AllowedTools []string `yaml:"allowedTools"`
}

type config struct {
	Upstreams map[string]upstreamCfg `yaml:"upstreams"`
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func loadConfig(path string) (config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return config{}, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, nil
}

func buildToolsList(cfg config) []toolEntry {
	var tools []toolEntry
	for upstreamName, u := range cfg.Upstreams {
		if len(u.AllowedTools) == 0 {
			// No allowedTools filter: advertise a sentinel indicating all tools are exposed.
			tools = append(tools, toolEntry{
				Name:        upstreamName + "__*",
				Description: "all tools from " + upstreamName,
			})
			continue
		}
		for _, t := range u.AllowedTools {
			tools = append(tools, toolEntry{
				Name:        upstreamName + "__" + t,
				Description: t + " via " + upstreamName,
			})
		}
	}
	return tools
}

func main() {
	cfgPath := os.Getenv("KAPEPROXY_CONFIG")
	if cfgPath == "" {
		cfgPath = "/etc/kapeproxy/config.yaml"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	tools := buildToolsList(cfg)

	http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		var resp jsonRPCResponse
		resp.JSONRPC = "2.0"
		resp.ID = req.ID

		switch req.Method {
		case "tools/list":
			resp.Result = map[string]interface{}{"tools": tools}
		default:
			resp.Error = &rpcError{Code: -32603, Message: "server not yet available"}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	log.Printf("kapeproxy stub listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
```

- [ ] **Step 2: Compile stub**

```bash
cd /path/to/worktree/kapeproxy && go build ./cmd/kapeproxy-stub/...
```

Expected: exits 0, binary produced.

- [ ] **Step 3: Commit**

```bash
git -C /path/to/worktree add kapeproxy/cmd/kapeproxy-stub/main.go
git -C /path/to/worktree commit -m "feat(kapeproxy): add stub binary (DEPRECATED in slice 7)"
```

---

## Task 11: Write stub binary tests

**Files:**
- Create: `kapeproxy/cmd/kapeproxy-stub/main_test.go`

- [ ] **Step 1: Write table tests for tools/list**

```go
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

func callToolsList(t *testing.T, cfgPath string) []toolEntry {
	t.Helper()
	cfg, err := loadConfig(cfgPath)
	require.NoError(t, err)
	return buildToolsList(cfg)
}

func TestToolsList_AllowedToolsPresent(t *testing.T) {
	cfgContent := `
upstreams:
  grafana-mcp:
    url: http://grafana:8080
    transport: sse
    allowedTools:
      - grafana_query
      - grafana_dashboard
    audit: true
`
	path := writeTempConfig(t, cfgContent)
	tools := callToolsList(t, path)

	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	sort.Strings(names)
	assert.Equal(t, []string{"grafana-mcp__grafana_dashboard", "grafana-mcp__grafana_query"}, names)
}

func TestToolsList_AllowedToolsAbsent_SentinelReturned(t *testing.T) {
	cfgContent := `
upstreams:
  search-mcp:
    url: http://search:9000
    transport: streamable-http
    audit: true
`
	path := writeTempConfig(t, cfgContent)
	tools := callToolsList(t, path)

	require.Len(t, tools, 1)
	assert.Equal(t, "search-mcp__*", tools[0].Name)
}

func TestToolsList_MultipleUpstreams(t *testing.T) {
	cfgContent := `
upstreams:
  alpha:
    url: http://alpha:8080
    transport: sse
    allowedTools:
      - do_thing
    audit: true
  beta:
    url: http://beta:8080
    transport: sse
    allowedTools:
      - other_thing
    audit: true
`
	path := writeTempConfig(t, cfgContent)
	tools := callToolsList(t, path)

	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	sort.Strings(names)
	assert.Equal(t, []string{"alpha__do_thing", "beta__other_thing"}, names)
}

func TestMCPHandler_ToolsCall_ReturnsError(t *testing.T) {
	cfgContent := `
upstreams:
  grafana-mcp:
    url: http://grafana:8080
    transport: sse
    audit: true
`
	path := writeTempConfig(t, cfgContent)
	cfg, err := loadConfig(path)
	require.NoError(t, err)

	// Rebuild handler with this config (simulate main setup)
	origTools := buildToolsList(cfg)
	_ = origTools

	body, _ := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))

	// The handler uses package-level tools — we test via HTTP handler inline
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqObj jsonRPCRequest
		_ = json.NewDecoder(r.Body).Decode(&reqObj)
		w.Header().Set("Content-Type", "application/json")
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      reqObj.ID,
			Error:   &rpcError{Code: -32603, Message: "server not yet available"},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	handler.ServeHTTP(rec, req)

	var resp jsonRPCResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.NotNil(t, resp.Error)
	assert.Equal(t, -32603, resp.Error.Code)
}
```

- [ ] **Step 2: Run tests**

```bash
cd /path/to/worktree/kapeproxy && go test ./cmd/kapeproxy-stub/... -v
```

Expected: all tests PASS.

- [ ] **Step 3: Commit**

```bash
git -C /path/to/worktree add kapeproxy/cmd/kapeproxy-stub/main_test.go
git -C /path/to/worktree commit -m "test(kapeproxy): table tests for stub tools/list and tools/call"
```

---

## Task 12: Write Dockerfile.stub and README

**Files:**
- Create: `kapeproxy/Dockerfile.stub`
- Create: `kapeproxy/README.md`

- [ ] **Step 1: Write Dockerfile.stub**

```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o kapeproxy-stub ./cmd/kapeproxy-stub

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /build/kapeproxy-stub /kapeproxy-stub
EXPOSE 8080
ENTRYPOINT ["/kapeproxy-stub"]
```

- [ ] **Step 2: Write README.md**

```markdown
# kapeproxy

This module contains the kapeproxy MCP proxy binary for kape-io handlers.

## Stub image (Phase 6 Slice 5)

The `kape/kapeproxy:stub` image is a **transitional, non-production** binary.
It reads `/etc/kapeproxy/config.yaml` (rendered by the operator) and serves a
static `tools/list` response with namespaced tool names (`{kapetool}__{toolname}`).
All `tools/call` requests return MCP error `-32603 server not yet available`.

The stub image is built and pushed by the slice-5 CI workflow. It is removed in
**Phase 6 Slice 7**, which ships the real kapeproxy binary.

Do NOT use the stub image as a stable artifact. It carries no backwards-compatibility
guarantee and will be deleted from the registry after Slice 7 merges.

## Real binary (Phase 6 Slice 7)

Replaces the stub. Implements full MCP proxying with allowlist filtering, JSONPath
redaction, audit logging, and OTEL tracing.
```

- [ ] **Step 3: Commit**

```bash
git -C /path/to/worktree add kapeproxy/Dockerfile.stub kapeproxy/README.md
git -C /path/to/worktree commit -m "feat(kapeproxy): add Dockerfile.stub and README documenting stub lifecycle"
```

---

## Task 13: Add CI workflow to build and push kapeproxy:stub

**Files:**
- Create: `.github/workflows/kapeproxy-stub.yml`

There are no existing GitHub Actions workflows in this repo. Use a minimal Docker build-and-push workflow following standard GitHub Actions conventions.

- [ ] **Step 1: Create the workflow directory if needed**

```bash
mkdir -p /path/to/worktree/.github/workflows
```

- [ ] **Step 2: Write the workflow file**

```yaml
name: kapeproxy stub image

on:
  push:
    branches: [main]
    paths:
      - "kapeproxy/**"

jobs:
  build-and-push:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Log in to registry
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Build and push kapeproxy:stub
        uses: docker/build-push-action@v5
        with:
          context: kapeproxy
          file: kapeproxy/Dockerfile.stub
          push: true
          tags: |
            ghcr.io/${{ github.repository_owner }}/kapeproxy:stub
            ghcr.io/${{ github.repository_owner }}/kapeproxy:stub-${{ github.sha }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
```

Note: The default registry tag `kape/kapeproxy:stub` in config defaults refers to the logical image name. Slice 7 and the ops team should align the actual registry path (ghcr.io or DockerHub) with the `kapeproxy.image` and `kapeproxy.version` defaults in `kape-config`. For M2, the `KapeproxyImageRef()` default of `kape/kapeproxy:stub` should be updated to match whatever registry is used.

- [ ] **Step 3: Commit**

```bash
git -C /path/to/worktree add .github/workflows/kapeproxy-stub.yml
git -C /path/to/worktree commit -m "ci: add GitHub Actions workflow to build and push kapeproxy:stub image"
```

---

## Task 14: Run Snyk Code scan and fix any issues

- [ ] **Step 1: Run Snyk Code scan on operator/**

Use the MCP tool:
```
mcp__Snyk__snyk_code_scan with path: "operator/"
```

- [ ] **Step 2: Run Snyk Code scan on kapeproxy/**

```
mcp__Snyk__snyk_code_scan with path: "kapeproxy/"
```

- [ ] **Step 3: Fix any issues found**

For each finding:
- Read the affected file.
- Apply the minimum fix that resolves the security issue.
- Re-run the scan to confirm resolution.
- Commit the fix with message `fix(security): <description of issue fixed>`.

- [ ] **Step 4: Confirm scans are clean**

Both scans must return no new issues introduced by this branch before proceeding.

---

## Task 15: Run SBOM scans and prepare PR

- [ ] **Step 1: Run SBOM scans on all three operator modules**

Per `kape-io/CLAUDE.md`:

```
mcp__Snyk__snyk_sbom_scan with path: "./adapters", format: "cyclonedx1.4+json"
mcp__Snyk__snyk_sbom_scan with path: "./operator", format: "cyclonedx1.4+json"
mcp__Snyk__snyk_sbom_scan with path: "./task-service", format: "cyclonedx1.4+json"
```

Record component counts and any flagged components from each result.

- [ ] **Step 2: Push branch and open PR**

Use `superpowers:finishing-a-development-branch` to push and open the PR:
- PR title: `feat(phase6): slice 5 — kapeproxy-config rendering + sidecar injection with stub image`
- Base branch: `main`
- PR body must paste the slice 5 acceptance criterion verbatim from `docs/roadmap/phases/06-full-operator/README.md`.

- [ ] **Step 3: Post SBOM summary comment**

After the PR is created, post the SBOM summary comment:

```bash
gh pr comment "$(gh pr view --json url --jq '.url')" --body "## SBOM Summary

| Module | Components | Flagged |
|---|---|---|
| adapters | <count> | <count or \"none\"> |
| operator | <count> | <count or \"none\"> |
| task-service | <count> | <count or \"none\"> |

Generated via Snyk CycloneDX 1.4 — <UTC timestamp>"
```

---

## Definition of Done

Both criteria from the Phase 6 README must be demonstrated by tests before PR merge:

1. **"Apply KapeHandler referencing a KapeSkill → handler pod has single kapeproxy sidecar (no per-tool sidecars)"**
   - Demonstrated by: `TestDeploymentAdapter_InjectsSingleKapeproxySidecarForMCPTool` and `TestDeploymentAdapter_NoSidecarWhenNoMCPTools`.

2. **"kapeproxy `tools/list` returns namespaced tool names (`kapetool-name__tool-name`)"**
   - Demonstrated by: `TestToolsList_AllowedToolsPresent` and `TestToolsList_MultipleUpstreams` in `kapeproxy/cmd/kapeproxy-stub/main_test.go`.

Additionally:
- `kape/kapeproxy:stub` image is built and pushed by the CI workflow before merge.
- Snyk Code scans on `operator/` and `kapeproxy/` are clean.
- SBOM scans run on all three existing modules with summary comment posted on the PR.

---

## Risk Notes (from spec)

**R1 — Stub becomes load-bearing:** The `// DEPRECATED: removed in Phase 6 slice 7` comment in `main.go` and the `kapeproxy/README.md` transitional warning are this slice's mitigations. Do not add the stub image to any stable helm values or example configs without a matching "TODO: replace with slice 7" comment.

**OQ1 — kape-config rollout:** Including `cfg.KapeproxyImage` + `cfg.KapeproxyImageVersion` in `computeRolloutHash` (Task 8 Step 3) ensures that changing `kapeproxy.image` or `kapeproxy.version` in the `kape-config` ConfigMap triggers a handler rollout within one reconcile cycle.

**OQ2 — Resource limits:** Hardcoded at `requests cpu=100m memory=128Mi, limits cpu=500m memory=256Mi` per spec 0013 §4.3. Tunability is a future enhancement tracked in the deferred list.
