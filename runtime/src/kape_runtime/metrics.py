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

kape_action_results_total = Counter(
    "kape_action_results_total",
    "Total action results emitted by the runtime, partitioned by handler, action, type, and status.",
    labelnames=["handler", "action", "type", "status"],
)
