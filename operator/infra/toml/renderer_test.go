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
	assert.True(t, strings.Contains(content, `type = "mcp"`) || strings.Contains(content, "type = 'mcp'"), "should contain mcp type")
	assert.Contains(t, content, "sidecar_port = 8080")
	assert.True(t, strings.Contains(content, `transport = "sse"`) || strings.Contains(content, "transport = 'sse'"), "should contain sse transport")
}

func TestRenderer_IncludesMemoryToolSection(t *testing.T) {
	r := tomlrenderer.NewRenderer()
	content, err := r.Render(baseHandler(), baseSchema(), baseTools(), domainconfig.KapeConfig{})
	require.NoError(t, err)

	assert.Contains(t, content, "[tools.karpenter-memory]")
	assert.True(t, strings.Contains(content, `type = "memory"`) || strings.Contains(content, "type = 'memory'"), "should contain memory type")
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
