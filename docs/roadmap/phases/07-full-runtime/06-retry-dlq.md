# Phase 7.6 — Retry + DLQ

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0004

## Goal

Wrap LangGraph invocation with `tenacity` exponential backoff for LLM transient errors (429, 503). Route non-retryable errors immediately to a DLQ NATS subject.

## Work

- Wrap `graph.invoke()` call with `tenacity.retry`:
  - Retry on: `httpx.HTTPStatusError` with status 429 or 503; `openai.RateLimitError`
  - Wait: exponential backoff, start 1s, multiplier 2, max 30s
  - Stop: after 5 attempts
- Non-retryable errors (all others): catch, publish DLQ message, mark Task `failed`
- DLQ publish: NATS subject `kape.events.dlq.<handler-name>`, message body:
  ```json
  {
    "original_event": "<base64 CloudEvent>",
    "error": "<exception type: message>",
    "task_id": "<uuid>",
    "handler": "<handler-name>",
    "timestamp": "<ISO-8601>"
  }
  ```
- Create `runtime/dlq.py` for DLQ publish logic

## Acceptance Criteria

- Mocked LLM 429 response → retry fires; succeeds on retry → Task `completed`
- Non-retryable error → Task `failed`; message appears on `kape.events.dlq.<name>` with correct fields
- After 5 failed retries → routed to DLQ

## Key Files

- `runtime/graph/graph.py` (modified — wrap invoke with retry)
- `runtime/dlq.py` (new)
- `runtime/tests/test_retry.py` (new)
