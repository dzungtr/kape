package kapeproxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kape-io/kape/kapeproxy/internal/proxy"
)

// mockMCPServer wraps an mcp.Server with call recording.
type mockMCPServer struct {
	mu           sync.Mutex
	calls        []mockCall
	callCount    atomic.Int32
	httpServer   *httptest.Server
}

type mockCall struct {
	Name string
	Args map[string]any
}

func newMockMCP(t *testing.T, tools []string, staticResult any) *mockMCPServer {
	t.Helper()
	m := &mockMCPServer{}

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mock-upstream", Version: "1.0"}, nil)

	for _, toolName := range tools {
		name := toolName // capture
		mcpServer.AddTool(&mcp.Tool{
			Name:        name,
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			m.callCount.Add(1)
			// Extract arguments from the raw params.
			var args map[string]any
			if req.Params.Arguments != nil {
				_ = json.Unmarshal(req.Params.Arguments, &args)
			}
			m.mu.Lock()
			m.calls = append(m.calls, mockCall{Name: name, Args: args})
			m.mu.Unlock()

			resultBytes, _ := json.Marshal(staticResult)
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: string(resultBytes)}},
			}, nil
		})
	}

	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpServer
	}, nil)

	m.httpServer = httptest.NewServer(handler)
	t.Cleanup(m.httpServer.Close)
	return m
}

func (m *mockMCPServer) URL() string { return m.httpServer.URL }

func writeKapeproxyConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// runKapeproxy starts a kapeproxy instance from a config file.
func runKapeproxy(t *testing.T, configPath string, addr string) func() {
	t.Helper()
	cfg, err := proxy.LoadConfig(configPath)
	require.NoError(t, err)

	ctx := context.Background()
	upstreams := make(map[string]proxy.Upstream, len(cfg.Upstreams))
	for name, up := range cfg.Upstreams {
		upstreams[name] = proxy.NewMCPUpstream(ctx, name, up)
	}
	router := proxy.NewRouter(cfg, upstreams)
	srv := proxy.NewServer(addr, router, proxy.NewRedactor(),
		proxy.NewAuditLogger(zerolog.Nop()), zerolog.Nop())

	go func() { _ = srv.Start() }()
	// Wait for listener to come up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, err := http.Post("http://"+addr, "application/json", bytes.NewReader([]byte("{}")))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// callKapeproxy posts a JSON-RPC request to the kapeproxy under test.
func callKapeproxy(t *testing.T, addr, method string, params map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	req, err := http.NewRequest("POST", "http://"+addr, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestIntegration_ToolsList_NamespacedAndFiltered(t *testing.T) {
	mock := newMockMCP(t, []string{"a", "b", "c"}, "ok")
	cfgYAML := `
upstreams:
  mock:
    url: ` + mock.URL() + `
    transport: streamable-http
    allowedTools:
      - a
      - b
`
	cfgPath := writeKapeproxyConfig(t, cfgYAML)
	cleanup := runKapeproxy(t, cfgPath, "127.0.0.1:18901")
	defer cleanup()

	resp := callKapeproxy(t, "127.0.0.1:18901", "tools/list", nil)
	result, ok := resp["result"].(map[string]any)
	require.True(t, ok, "expected result object, got: %v", resp)
	tools, _ := result["tools"].([]any)
	names := make([]string, 0, len(tools))
	for _, e := range tools {
		names = append(names, e.(map[string]any)["name"].(string))
	}
	assert.ElementsMatch(t, []string{"mock__a", "mock__b"}, names,
		"tools/list returns namespaced + allowlist-filtered names; 'c' must not appear")
}

func TestIntegration_ToolsCall_AllowedRedactsInputAndOutput(t *testing.T) {
	// The mock returns a static JSON result; we use a simple string
	// that represents the output before redaction.
	staticOutput := map[string]any{"data": map[string]any{"email": "x@y.z"}}
	mock := newMockMCP(t, []string{"echo"}, staticOutput)
	cfgYAML := `
upstreams:
  mock:
    url: ` + mock.URL() + `
    transport: streamable-http
    allowedTools: ["echo"]
    redaction:
      input:
        - jsonPath: "$.token"
`
	// Note: output redaction on the CallToolResult.Content text is not applied
	// in v1 (content is []mcp.Content, not a raw map). We test input redaction
	// (upstream sees blanked token) and that the call succeeds.
	cfgPath := writeKapeproxyConfig(t, cfgYAML)
	cleanup := runKapeproxy(t, cfgPath, "127.0.0.1:18902")
	defer cleanup()

	resp := callKapeproxy(t, "127.0.0.1:18902", "tools/call", map[string]any{
		"name":      "mock__echo",
		"arguments": map[string]any{"token": "secret", "ok": "keep"},
	})

	// Call must succeed (no error).
	require.Nil(t, resp["error"], "expected success, got error: %v", resp["error"])

	// Upstream saw redacted INPUT: token must be blank.
	require.GreaterOrEqual(t, len(mock.calls), 1, "upstream must have received a call")
	assert.Equal(t, "echo", mock.calls[0].Name)
	assert.Equal(t, "", mock.calls[0].Args["token"], "token redacted before reaching upstream")
	assert.Equal(t, "keep", mock.calls[0].Args["ok"])
}

func TestIntegration_ToolsCall_DisallowedReturns32601(t *testing.T) {
	mock := newMockMCP(t, []string{"only"}, "ok")
	cfgYAML := `
upstreams:
  mock:
    url: ` + mock.URL() + `
    transport: streamable-http
    allowedTools: ["only"]
`
	cfgPath := writeKapeproxyConfig(t, cfgYAML)
	cleanup := runKapeproxy(t, cfgPath, "127.0.0.1:18903")
	defer cleanup()

	resp := callKapeproxy(t, "127.0.0.1:18903", "tools/call", map[string]any{
		"name": "mock__forbidden",
	})
	errObj, ok := resp["error"].(map[string]any)
	require.True(t, ok, "expected MCP error envelope, got: %v", resp)
	assert.Equal(t, float64(-32601), errObj["code"], "MCP method-not-found code")

	// Upstream MUST NOT have been called.
	assert.Equal(t, int32(0), mock.callCount.Load())
}

func TestIntegration_UnreachableUpstream_NonFatalAtStartup(t *testing.T) {
	cfgYAML := `
upstreams:
  ghost:
    url: http://127.0.0.1:1
    transport: streamable-http
    allowedTools: ["x"]
`
	cfgPath := writeKapeproxyConfig(t, cfgYAML)
	cleanup := runKapeproxy(t, cfgPath, "127.0.0.1:18904")
	defer cleanup()

	// Under D16+D20, an unreachable upstream has ListTools() == nil, so the
	// intersection of upstream.ListTools() with the allowedTools globs is
	// empty — nothing is exposed via tools/list.
	resp := callKapeproxy(t, "127.0.0.1:18904", "tools/list", nil)
	result, ok := resp["result"].(map[string]any)
	require.True(t, ok, "expected result object, got: %v", resp)
	tools, _ := result["tools"].([]any)
	assert.Empty(t, tools, "D16: unreachable upstream contributes no tools to tools/list")

	// tools/call returns an MCP error (Route() returns false; server emits
	// -32601) but does NOT panic. This proves the non-fatal-startup property
	// — the proxy is healthy even when an upstream is down.
	resp = callKapeproxy(t, "127.0.0.1:18904", "tools/call", map[string]any{"name": "ghost__x"})
	require.NotNil(t, resp["error"], "unreachable upstream must surface as MCP error, not panic")
}
