# Phase 7.7 — Dedup + Prometheus Metrics

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0004

## Goal

Add a sliding-window dedup cache to discard duplicate CloudEvent IDs within a 60-second window. Add Prometheus metrics for events, LLM latency, tool calls, and decisions.

## Work

### Dedup
- Create `runtime/dedup.py`:
  - In-memory dict of `event_id → expiry_timestamp`
  - `is_duplicate(event_id: str) -> bool`: returns True if id seen within 60s; registers id with 60s TTL
  - Sweep expired entries on each check (no background thread needed)
- In `consumer.py`: check `is_duplicate(event.id)` before processing; if True, ACK and skip

### Prometheus Metrics
- Create `runtime/metrics.py` with `prometheus_client` counters/histograms:
  - `kape_events_total` — Counter, labels: `handler`, `status` (`processed`, `deduplicated`, `failed`)
  - `kape_llm_latency_seconds` — Histogram, labels: `handler`
  - `kape_tool_calls_total` — Counter, labels: `handler`, `tool`
  - `kape_decisions_total` — Counter, labels: `handler`, `decision`
- Expose via existing FastAPI `/metrics` endpoint in `probe.py` using `prometheus_client.generate_latest()`

## Acceptance Criteria

- Same CloudEvent ID published twice within 60s → second is ACKed and discarded, no duplicate Task
- Same CloudEvent ID published after 60s → processed normally
- `curl handler-pod:8000/metrics` returns Prometheus text with all four metric names
- All metrics labelled by handler name

## Key Files

- `runtime/dedup.py` (new)
- `runtime/metrics.py` (new)
- `runtime/consumer.py` (modified — dedup check + metrics)
- `runtime/probe.py` (modified — /metrics endpoint)
- `runtime/tests/test_dedup.py` (new)
