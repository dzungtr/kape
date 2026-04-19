# Phase 9.2 — Task List + SSE

**Status:** pending
**Phase:** 09-dashboard
**Milestone:** M5
**Specs:** 0009

## Goal

Build the live task feed route: server-side loader for initial data, `EventSource` client connection to `GET /tasks/stream` for real-time updates, infinite scroll for history.

## Work

- `dashboard/app/routes/tasks._index.tsx`:
  - Server loader: `GET /tasks?limit=50` → initial task list
  - Client: `new EventSource("/api/tasks/stream")` via a `useEffect`; prepend new tasks to list on SSE event
  - Infinite scroll: `IntersectionObserver` on last list item → fetch next page
- `dashboard/app/components/TaskCard.tsx`:
  - Display: task ID (truncated), handler name, status badge (colour-coded), created_at relative time, `schema_output` decision summary (first 80 chars)
- Use generated types from `api.ts` for `Task` shape

## Acceptance Criteria

- Live task feed shows Tasks appearing in real-time as events are processed
- Scrolling to bottom loads next page of older tasks
- Status badge colour: `completed` = green, `failed` = red, `running` = yellow, `pending` = grey

## Key Files

- `dashboard/app/routes/tasks._index.tsx` (new)
- `dashboard/app/components/TaskCard.tsx` (new)
