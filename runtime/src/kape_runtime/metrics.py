# runtime/src/kape_runtime/metrics.py
from __future__ import annotations

from prometheus_client import Counter, Histogram

kape_events_total = Counter(
    "kape_events_total",
    "Total CloudEvents seen by the runtime, partitioned by handler and outcome.",
    labelnames=["handler", "status"],
)

kape_llm_latency_seconds = Histogram(
    "kape_llm_latency_seconds",
    "End-to-end LLM/graph invocation latency in seconds, partitioned by handler.",
    labelnames=["handler"],
)

kape_tool_calls_total = Counter(
    "kape_tool_calls_total",
    "Total tool calls executed by the runtime, partitioned by handler and tool name.",
    labelnames=["handler", "tool"],
)

# Population deferred: KapeSchema is user-defined per handler, so the runtime cannot assume
# a "decision" key exists in schema_output. A follow-up issue will introduce a
# KapeHandler-level pointer (or KapeSchema annotation) naming the field to source from.
kape_decisions_total = Counter(
    "kape_decisions_total",
    "Total schema-output decisions emitted by the runtime, partitioned by handler and decision.",
    labelnames=["handler", "decision"],
)
