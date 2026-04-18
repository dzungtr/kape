# runtime/src/kape_runtime/tracing.py
from __future__ import annotations

import logging

from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from openinference.instrumentation.langchain import LangChainInstrumentor

from kape_runtime.config import OtelConfig

logger = logging.getLogger(__name__)


def setup_tracing(
    config: OtelConfig,
    handler: str,
    cluster: str,
    namespace: str,
) -> None:
    """Configure global OTEL TracerProvider and auto-instrument LangChain/LangGraph."""
    resource = Resource.create(
        {
            "service.name": config.service_name,
            "kape.handler": handler,
            "kape.cluster": cluster,
            "kape.namespace": namespace,
        }
    )

    provider = TracerProvider(resource=resource)
    provider.add_span_processor(
        BatchSpanProcessor(OTLPSpanExporter(endpoint=config.endpoint))
    )
    trace.set_tracer_provider(provider)

    LangChainInstrumentor().instrument()
    logger.info("OTEL tracing configured; exporting to %s", config.endpoint)


def get_trace_id() -> str | None:
    """Return hex trace_id of the currently active span, or None if no active span."""
    span = trace.get_current_span()
    ctx = span.get_span_context()
    if ctx is None or not ctx.is_valid:
        return None
    return format(ctx.trace_id, "032x")
