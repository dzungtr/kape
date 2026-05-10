package k8s_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
				Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://secure:8080"},
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
	allTools := []v1alpha1.KapeTool{
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

	var mcpTools []v1alpha1.KapeTool
	for _, tool := range allTools {
		if tool.Spec.Type == "mcp" {
			mcpTools = append(mcpTools, tool)
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

	aIdx := strings.Index(got, "a-tool:")
	zIdx := strings.Index(got, "z-tool:")
	assert.Less(t, aIdx, zIdx, "tools should be sorted alphabetically")
}
