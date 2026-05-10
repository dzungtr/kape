package proxy

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "kape.io/kapeproxy"

// SpanName is the fixed name for kapeproxy tool-call spans.
const SpanName = "kapeproxy.tool_call"

// CallAttrs carries the attributes recorded on the kapeproxy.tool_call span.
type CallAttrs struct {
	NamespacedName string
	Upstream       string
	OriginalName   string
	Allowed        bool
	TaskID         string
}

// InitTracer wires the OTLP HTTP exporter and W3C TraceContext propagator.
// Returns a shutdown function the caller must defer.
//
// If OTEL_EXPORTER_OTLP_ENDPOINT is unset the exporter uses the SDK default endpoint.
func InitTracer(ctx context.Context) (func(context.Context) error, error) {
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("otlptracehttp.New: %w", err)
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName("kapeproxy")),
	)
	if err != nil {
		return nil, fmt.Errorf("resource.New: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return tp.Shutdown, nil
}

// StartCallSpan begins a "kapeproxy.tool_call" span with the required attributes.
// Pair with FinishCallSpan + span.End() in the caller.
func StartCallSpan(ctx context.Context, a CallAttrs) (context.Context, trace.Span) {
	tr := otel.Tracer(tracerName)
	ctx, span := tr.Start(ctx, SpanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("tool.namespaced_name", a.NamespacedName),
			attribute.String("tool.upstream", a.Upstream),
			attribute.String("tool.original_name", a.OriginalName),
			attribute.Bool("tool.allowed", a.Allowed),
			attribute.String("kape.task_id", a.TaskID),
		),
	)
	return ctx, span
}

// FinishCallSpan attaches latency + (optional) error info and ends the span.
// Pass err=nil for a successful call.
func FinishCallSpan(span trace.Span, latencyMS int64, err error) {
	span.SetAttributes(attribute.Int64("tool.latency_ms", latencyMS))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
