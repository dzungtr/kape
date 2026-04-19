# Phase 9.3 — Task Detail

**Status:** pending
**Phase:** 09-dashboard
**Milestone:** M5
**Specs:** 0009

## Goal

Build the task detail route showing full event payload, LLM structured decision, action results timeline, and Arize trace link.

## Work

- `dashboard/app/routes/tasks.$id.tsx`:
  - Server loader: `GET /tasks/{id}` → full Task record
  - Sections:
    1. **Event Payload**: pretty-printed JSON of `event_raw` field
    2. **LLM Decision**: pretty-printed JSON of `schema_output` field
    3. **Action Timeline**: list of actions from `action_results` JSONB — name, status (success/fail), timestamp
    4. **Trace**: link to Arize trace using `trace_id` field — `https://app.arize.com/...?traceId={trace_id}` (URL template from env var `ARIZE_BASE_URL`)
  - If `trace_id` is null: show "No trace available"
- `dashboard/app/components/JsonViewer.tsx`: reusable collapsible JSON display component (used in sections 1 and 2)

## Acceptance Criteria

- Task detail view renders all four sections for a completed Task
- JSON sections are collapsible
- Arize trace link opens in new tab
- 404 returned for non-existent task ID → rendered error page

## Key Files

- `dashboard/app/routes/tasks.$id.tsx` (new)
- `dashboard/app/components/JsonViewer.tsx` (new)
