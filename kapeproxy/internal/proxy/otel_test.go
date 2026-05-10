package proxy_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/kape-io/kape/kapeproxy/internal/proxy"
)

func setupRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return rec
}

func TestStartCallSpan_EmitsRequiredAttributes(t *testing.T) {
	rec := setupRecorder(t)
	ctx := context.Background()

	ctx, span := proxy.StartCallSpan(ctx, proxy.CallAttrs{
		NamespacedName: "grafana-mcp__query_dashboards",
		Upstream:       "grafana-mcp",
		OriginalName:   "query_dashboards",
		Allowed:        true,
		TaskID:         "task-1",
	})
	span.End()
	_ = ctx

	spans := rec.Ended()
	require.Len(t, spans, 1)
	s := spans[0]
	assert.Equal(t, "kapeproxy.tool_call", s.Name())
	attrs := attrMap(s.Attributes())
	assert.Equal(t, "grafana-mcp__query_dashboards", attrs["tool.namespaced_name"].AsString())
	assert.Equal(t, "grafana-mcp", attrs["tool.upstream"].AsString())
	assert.Equal(t, "query_dashboards", attrs["tool.original_name"].AsString())
	assert.Equal(t, true, attrs["tool.allowed"].AsBool())
	assert.Equal(t, "task-1", attrs["kape.task_id"].AsString())
}

func TestStartCallSpan_RecordsLatencyAndError(t *testing.T) {
	rec := setupRecorder(t)
	ctx, span := proxy.StartCallSpan(context.Background(), proxy.CallAttrs{
		NamespacedName: "x__y", Upstream: "x", OriginalName: "y", Allowed: false,
	})
	proxy.FinishCallSpan(span, 123, assertError("blocked by allowlist"))
	_ = ctx

	spans := rec.Ended()
	require.Len(t, spans, 1)
	attrs := attrMap(spans[0].Attributes())
	assert.Equal(t, int64(123), attrs["tool.latency_ms"].AsInt64())

	require.NotEmpty(t, spans[0].Events())
	require.Equal(t, "exception", spans[0].Events()[0].Name)
}

func TestExtractAndPropagateTraceContext(t *testing.T) {
	setupRecorder(t)

	// Inbound: a header-bearing carrier is extracted into ctx.
	carrier := propagation.MapCarrier{
		"traceparent": "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
	}
	ctx := otel.GetTextMapPropagator().Extract(context.Background(), carrier)

	// StartCallSpan must keep the inbound parent.
	_, span := proxy.StartCallSpan(ctx, proxy.CallAttrs{NamespacedName: "x__y"})
	defer span.End()
	sc := span.SpanContext()
	assert.Equal(t, "0af7651916cd43dd8448eb211c80319c", sc.TraceID().String(),
		"trace ID inherited from inbound traceparent")
}

func attrMap(in []attribute.KeyValue) map[string]attribute.Value {
	m := make(map[string]attribute.Value, len(in))
	for _, kv := range in {
		m[string(kv.Key)] = kv.Value
	}
	return m
}

func assertError(msg string) error { return &recErr{msg} }

type recErr struct{ s string }

func (e *recErr) Error() string { return e.s }
