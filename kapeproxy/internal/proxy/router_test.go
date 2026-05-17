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

// TestRouterList_NilAllowlistExposesNothing pins D20: nil allowedTools is
// deny-by-default — the upstream contributes nothing.
func TestRouterList_NilAllowlistExposesNothing(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: nil,
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "k8s_list_namespaces", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	assert.Empty(t, r.List(),
		"D20: nil allowedTools exposes zero tools (deny-by-default), regardless of what the upstream advertises")
}

// TestRouterList_EmptyAllowlistExposesNothing pins D20: an explicitly-empty
// allowedTools slice is equivalent to nil.
func TestRouterList_EmptyAllowlistExposesNothing(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: []string{},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	assert.Empty(t, r.List(),
		"D20: empty allowedTools slice exposes zero tools (deny-by-default)")
}

// TestRouterList_StarAllowlistExposesAll pins D20 escape hatch: ["*"] is the
// explicit opt-in to expose every upstream tool.
func TestRouterList_StarAllowlistExposesAll(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: []string{"*"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "k8s_list_namespaces", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	assert.ElementsMatch(t,
		[]string{"up__k8s_get_pods", "up__k8s_list_namespaces", "up__helm_install"},
		r.List(),
		"D20: [\"*\"] is the legacy 'expose all' opt-in; matches every flat tool name")
}

// TestRouterList_GlobIntersectsUpstream pins D16: glob patterns intersected
// with upstream.ListTools().
func TestRouterList_GlobIntersectsUpstream(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: []string{"k8s_*"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "k8s_list_namespaces", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	assert.ElementsMatch(t,
		[]string{"up__k8s_get_pods", "up__k8s_list_namespaces"},
		r.List(),
		"only k8s_* tools that the upstream actually advertises should be exposed",
	)
}

// TestRouterList_ExactNameNotOnUpstream_Dropped pins D16: exact-name entries
// not on the upstream get silently dropped.
func TestRouterList_ExactNameNotOnUpstream_Dropped(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "sse",
				AllowedTools: []string{"k8s_get_pods", "nonexistent"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "k8s_list_namespaces", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	assert.ElementsMatch(t,
		[]string{"up__k8s_get_pods"},
		r.List(),
		"exact-name allowlist entries that don't match any upstream tool are silently dropped",
	)
}

// TestRouterRoute_NilAllowlistDenies pins D20: nil allowedTools → tools/call
// is rejected for every namespaced name from this upstream.
func TestRouterRoute_NilAllowlistDenies(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: nil,
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	_, ok := r.Route("up__k8s_get_pods")
	assert.False(t, ok, "D20: nil allowedTools denies tools/call even when upstream has the tool")
	_, ok = r.Route("up__anything")
	assert.False(t, ok, "D20: nil allowedTools denies tools/call for every name on this upstream")
}

// TestRouterRoute_StarAllowlistAllows pins D20 escape hatch: ["*"] allows
// every upstream tool through Route() as well as List().
func TestRouterRoute_StarAllowlistAllows(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: []string{"*"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	e, ok := r.Route("up__k8s_get_pods")
	require.True(t, ok)
	assert.Equal(t, "k8s_get_pods", e.OriginalName)

	e, ok = r.Route("up__helm_install")
	require.True(t, ok)
	assert.Equal(t, "helm_install", e.OriginalName)
}

// TestRouterRoute_GlobIntersectsUpstream pins D16: Route() requires both
// (a) the original name is on upstream.ListTools(), and (b) it matches at
// least one glob in allowedTools.
func TestRouterRoute_GlobIntersectsUpstream(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: []string{"k8s_*"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "k8s_list_namespaces", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	// Matches glob AND exists on upstream → routable.
	e, ok := r.Route("up__k8s_get_pods")
	require.True(t, ok)
	assert.Equal(t, "k8s_get_pods", e.OriginalName)

	// Exists on upstream but does NOT match any glob → not routable.
	_, ok = r.Route("up__helm_install")
	assert.False(t, ok, "helm_install is on the upstream but no glob matches it")

	// Matches glob (vacuously) but does NOT exist on upstream → not routable.
	_, ok = r.Route("up__k8s_nonexistent")
	assert.False(t, ok, "k8s_nonexistent matches k8s_* but is not on the upstream")
}

// TestRouterRoute_MalformedGlob_DoesNotPanic pins the spec's error handling
// for path.Match returning ErrBadPattern: skip the malformed pattern, do not
// take the proxy offline.
func TestRouterRoute_MalformedGlob_DoesNotPanic(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "sse",
				AllowedTools: []string{"[bad"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{name: "up", tools: []string{"anything"}, avail: true},
	}
	r := proxy.NewRouter(cfg, ups)
	assert.Empty(t, r.List(), "malformed glob matches nothing; router must not panic")
	_, ok := r.Route("up__anything")
	assert.False(t, ok)
}

// TestRouter_UnavailableUpstream_ExposesNothing replaces the pre-D16
// TestRouter_UnavailableUpstream_StillExposesNames. Under D16, the exposed
// set is intersection(upstream.ListTools, globs). An unreachable upstream
// has ListTools() == nil, so nothing can be exposed regardless of what's in
// allowedTools.
func TestRouter_UnavailableUpstream_ExposesNothing(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"down-mcp": {
				URL: "http://down:8080", Transport: "sse",
				AllowedTools: []string{"foo"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"down-mcp": &fakeUpstream{name: "down-mcp", tools: nil, avail: false},
	}
	r := proxy.NewRouter(cfg, ups)
	assert.Empty(t, r.List(),
		"D16: unreachable upstream → empty ListTools → empty intersection → nothing exposed")
	_, ok := r.Route("down-mcp__foo")
	assert.False(t, ok)
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
	// Upstream must advertise "t" under D16, otherwise Route() returns false.
	ups := map[string]proxy.Upstream{"x": &fakeUpstream{name: "x", tools: []string{"t"}, avail: true}}
	r := proxy.NewRouter(cfg, ups)
	e, ok := r.Route("x__t")
	require.True(t, ok)
	require.NotNil(t, e.Redaction)
	require.Len(t, e.Redaction.Input, 1)
	assert.Equal(t, "$.s", e.Redaction.Input[0].JSONPath)
	assert.False(t, e.Audit, "audit:false propagates")
}
