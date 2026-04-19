# Phase 4 — Minimal Runtime

**Status:** done
**Milestone:** —
**Specs:** 0001, 0004
**Modified by:** 0012 (created)

## Goal

A handler pod that connects to NATS, consumes a CloudEvent, runs the LangGraph agent (no MCP tools yet), produces a structured decision, and writes the full Task lifecycle to the task-service.

## Reference Specs

- `0001-rfc` — overall agent loop design, event flow, two-phase execution model
- `0004-kape-handler` — 7-layer execution model, LangGraph graph structure, Task lifecycle, OTEL spans, settings.toml schema

## Work

- `config.py`: dynaconf loader — reads `settings.toml` + env var overrides → typed `Config` dataclass
- `consumer.py`: NATS JetStream pull consumer loop — explicit ACK strategy
- `graph/graph.py`: minimal LangGraph graph — `entry_router → reason → respond`
- `models.py`: `AgentState`, `Task`, `TaskStatus`, `ActionResult` Pydantic models
- `task_service.py`: httpx async client for task-service API
- `tracing.py`: OTEL setup with `openinference-instrumentation-langchain`
- `probe.py`: FastAPI `/healthz` + `/readyz`
- Task lifecycle: `pending → running → completed/failed/low-confidence`

**Not in this phase:** MCP tool nodes, memory tools, event-publish, ActionsRouter, retry/DLQ, Prometheus metrics.

## Acceptance Criteria

- Publish a test CloudEvent to NATS manually
- Handler pod picks up the event
- Task record transitions `pending → running → completed`
- `schema_output` JSONB contains the LLM's structured decision
- OTEL span visible in local Jaeger

## Key Files

- `runtime/config.py`
- `runtime/consumer.py`
- `runtime/graph/graph.py`, `runtime/graph/nodes.py`, `runtime/graph/state.py`
- `runtime/task_service.py`
- `runtime/tracing.py`
- `runtime/probe.py`
