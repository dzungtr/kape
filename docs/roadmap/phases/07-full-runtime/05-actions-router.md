# Phase 7.5 — Actions Router

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0004, 0006

## Goal

Implement the `ActionsRouter` that runs post-decision actions against `schema_output`. Supports `event-publish`, `webhook`, and `save_memory` action types. All configured actions run; partial failures are logged but do not fail the Task.

## Work

- `runtime/actions/router.py`:
  - Accept `schema_output` dict and list of configured actions
  - Evaluate JSONPath condition for each action; skip if condition is falsy
  - Dispatch to appropriate handler: `event_emitter`, `webhook`, `save_memory`
  - Catch exceptions per action; log error; continue to next action

- `runtime/actions/event_emitter.py`:
  - Resolve Jinja2 template fields in action config against `schema_output`
  - Extract `$prompt` fields (special marker for prompt passthrough)
  - Build CloudEvent with resolved fields
  - Publish to NATS subject from action config
  - Set `parent_task_id` on outgoing CloudEvent data

- `runtime/actions/webhook.py`:
  - HTTP POST via `httpx` to configured `url`
  - Body: Jinja2-rendered JSON template resolved against `schema_output`
  - Timeout: 10s

- `runtime/actions/save_memory.py`:
  - Upsert document to Qdrant collection
  - Document text: Jinja2-rendered template from action config
  - Metadata: `handler_name`, `task_id`, `timestamp`

## Acceptance Criteria

- `event-publish` action: Handler A fires → CloudEvent published → Handler B picks it up; both Task records exist with correct `parent_task_id`
- `webhook` action: HTTP POST to test server with rendered body confirmed
- `save_memory` action: document upserted to Qdrant with correct metadata
- One action failing does not prevent remaining actions from running

## Key Files

- `runtime/actions/router.py` (new)
- `runtime/actions/event_emitter.py` (new)
- `runtime/actions/webhook.py` (new)
- `runtime/actions/save_memory.py` (new)
- `runtime/tests/test_actions.py` (new)
