package proxy_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kape-io/kape/kapeproxy/internal/proxy"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func TestLoadConfig_FullExample(t *testing.T) {
	yaml := `
upstreams:
  grafana-mcp:
    url: http://grafana-mcp:8080
    transport: streamable-http
    allowedTools:
      - query_dashboards
      - get_alert
    redaction:
      input:
        - jsonPath: "$.token"
      output:
        - jsonPath: "$.data.email"
    audit: false
  basic-mcp:
    url: http://basic:8080
    transport: sse
`
	p := writeTempConfig(t, yaml)
	cfg, err := proxy.LoadConfig(p)
	require.NoError(t, err)
	require.Len(t, cfg.Upstreams, 2)

	g := cfg.Upstreams["grafana-mcp"]
	assert.Equal(t, "http://grafana-mcp:8080", g.URL)
	assert.Equal(t, "streamable-http", g.Transport)
	assert.Equal(t, []string{"query_dashboards", "get_alert"}, g.AllowedTools)
	assert.False(t, *g.Audit) // explicitly false
	require.NotNil(t, g.Redaction)
	require.Len(t, g.Redaction.Input, 1)
	assert.Equal(t, "$.token", g.Redaction.Input[0].JSONPath)
	require.Len(t, g.Redaction.Output, 1)
	assert.Equal(t, "$.data.email", g.Redaction.Output[0].JSONPath)

	b := cfg.Upstreams["basic-mcp"]
	assert.Equal(t, "http://basic:8080", b.URL)
	assert.Equal(t, "sse", b.Transport)
	assert.Nil(t, b.AllowedTools, "allowedTools omitted means nil (expose all)")
	require.NotNil(t, b.Audit)
	assert.True(t, *b.Audit, "audit defaults to true when omitted")
	assert.Nil(t, b.Redaction)
}

func TestLoadConfig_EmptyAllowedToolsTreatedAsNil(t *testing.T) {
	yaml := `
upstreams:
  empty-allow:
    url: http://x:8080
    transport: sse
    allowedTools: []
`
	p := writeTempConfig(t, yaml)
	cfg, err := proxy.LoadConfig(p)
	require.NoError(t, err)
	assert.Nil(t, cfg.Upstreams["empty-allow"].AllowedTools, "explicit empty list normalises to nil")
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := proxy.LoadConfig("/no/such/file.yaml")
	require.Error(t, err)
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	p := writeTempConfig(t, "upstreams: [this is not a map")
	_, err := proxy.LoadConfig(p)
	require.Error(t, err)
}
