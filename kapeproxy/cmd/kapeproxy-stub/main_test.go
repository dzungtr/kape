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
	body, _ := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))

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
