package proxy_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kape-io/kape/kapeproxy/internal/proxy"
)

func TestAuditLogger_AllowedCallShapesAllFields(t *testing.T) {
	buf := &bytes.Buffer{}
	zlog := zerolog.New(buf)
	a := proxy.NewAuditLogger(zlog)

	a.Log(proxy.AuditEntry{
		NamespacedName: "grafana-mcp__query_dashboards",
		Upstream:       "grafana-mcp",
		OriginalName:   "query_dashboards",
		Allowed:        true,
		LatencyMS:      42,
		TaskID:         "task-abc",
	})

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "grafana-mcp__query_dashboards", entry["tool.namespaced_name"])
	assert.Equal(t, "grafana-mcp", entry["tool.upstream"])
	assert.Equal(t, "query_dashboards", entry["tool.original_name"])
	assert.Equal(t, true, entry["tool.allowed"])
	assert.Equal(t, float64(42), entry["tool.latency_ms"])
	assert.Equal(t, "task-abc", entry["kape.task_id"])
	_, hasErr := entry["error"]
	assert.False(t, hasErr, "error field omitted when no error")
}

func TestAuditLogger_DisallowedCallSetsAllowedFalseAndError(t *testing.T) {
	buf := &bytes.Buffer{}
	zlog := zerolog.New(buf)
	a := proxy.NewAuditLogger(zlog)

	a.Log(proxy.AuditEntry{
		NamespacedName: "x__forbidden",
		Upstream:       "x",
		OriginalName:   "forbidden",
		Allowed:        false,
		LatencyMS:      0,
		Error:          "tool not in allowedTools",
	})

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, false, entry["tool.allowed"])
	assert.Equal(t, "tool not in allowedTools", entry["error"])
	_, hasTask := entry["kape.task_id"]
	assert.False(t, hasTask, "kape.task_id omitted when empty")
}

func TestAuditLogger_SkipsWhenAuditDisabled(t *testing.T) {
	buf := &bytes.Buffer{}
	zlog := zerolog.New(buf)
	a := proxy.NewAuditLogger(zlog)

	a.LogIfEnabled(false, proxy.AuditEntry{NamespacedName: "x__y"})
	assert.Equal(t, 0, buf.Len(), "no log line when audit disabled")

	a.LogIfEnabled(true, proxy.AuditEntry{NamespacedName: "x__y"})
	assert.Greater(t, buf.Len(), 0, "log line emitted when audit enabled")
}
