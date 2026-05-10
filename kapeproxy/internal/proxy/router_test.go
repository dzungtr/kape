package proxy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kape-io/kape/kapeproxy/internal/proxy"
)

// fakeUpstream is a minimal Upstream stand-in used only by the router tests.
type fakeUpstream struct {
	name  string
	tools []string // names of all upstream tools (for Available())
	avail bool
}

func (f *fakeUpstream) Name() string        { return f.name }
func (f *fakeUpstream) Available() bool     { return f.avail }
func (f *fakeUpstream) ListTools() []string { return f.tools }
func (f *fakeUpstream) CallTool(_ string, _ string, _ map[string]any) (any, error) {
	panic("router tests don't call upstreams")
}
func (f *fakeUpstream) Close() error { return nil }

func TestRouter_NamespacedNames_WithAllowlist(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"grafana-mcp": {
				URL:          "http://grafana:8080",
				Transport:    "streamable-http",
				AllowedTools: []string{"query_dashboards", "get_alert"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"grafana-mcp": &fakeUpstream{
			name:  "grafana-mcp",
			tools: []string{"query_dashboards", "get_alert", "delete_dashboard"}, // upstream has 3
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	names := r.List()
	assert.ElementsMatch(t, []string{
		"grafana-mcp__query_dashboards",
		"grafana-mcp__get_alert",
	}, names, "delete_dashboard must NOT appear (filtered by allowedTools)")

	e, ok := r.Route("grafana-mcp__query_dashboards")
	require.True(t, ok)
	assert.Equal(t, "grafana-mcp", e.Upstream.Name())
	assert.Equal(t, "query_dashboards", e.OriginalName)
}

func TestRouter_NoAllowlist_ExposesAllUpstreamTools(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"basic-mcp": {URL: "http://basic:8080", Transport: "sse"}, // no AllowedTools
		},
	}
	ups := map[string]proxy.Upstream{
		"basic-mcp": &fakeUpstream{name: "basic-mcp", tools: []string{"a", "b"}, avail: true},
	}
	r := proxy.NewRouter(cfg, ups)
	assert.ElementsMatch(t, []string{"basic-mcp__a", "basic-mcp__b"}, r.List())
}

func TestRouter_UnavailableUpstream_StillExposesNames(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"down-mcp": {URL: "http://down:8080", Transport: "sse", AllowedTools: []string{"foo"}},
		},
	}
	ups := map[string]proxy.Upstream{
		"down-mcp": &fakeUpstream{name: "down-mcp", tools: nil, avail: false},
	}
	r := proxy.NewRouter(cfg, ups)
	assert.ElementsMatch(t, []string{"down-mcp__foo"}, r.List(), "allowedTools is the source of truth when set")
}

func TestRouter_RouteMiss(t *testing.T) {
	cfg := &proxy.Config{Upstreams: map[string]*proxy.UpstreamConfig{}}
	r := proxy.NewRouter(cfg, map[string]proxy.Upstream{})
	_, ok := r.Route("does-not-exist__foo")
	assert.False(t, ok)
}

func TestRouter_RedactionAndAuditExposed(t *testing.T) {
	auditFalse := false
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"x": {
				URL: "u", Transport: "sse",
				AllowedTools: []string{"t"},
				Redaction: &proxy.RedactionConfig{
					Input: []proxy.JSONPathRule{{JSONPath: "$.s"}},
				},
				Audit: &auditFalse,
			},
		},
	}
	ups := map[string]proxy.Upstream{"x": &fakeUpstream{name: "x", avail: true}}
	r := proxy.NewRouter(cfg, ups)
	e, ok := r.Route("x__t")
	require.True(t, ok)
	require.NotNil(t, e.Redaction)
	require.Len(t, e.Redaction.Input, 1)
	assert.Equal(t, "$.s", e.Redaction.Input[0].JSONPath)
	assert.False(t, e.Audit, "audit:false propagates")
}
