# Phase 9.4 — Handler Routes

**Status:** pending
**Phase:** 09-dashboard
**Milestone:** M5
**Specs:** 0009

## Goal

Build the handler health overview page and handler detail page.

## Work

- `dashboard/app/routes/handlers._index.tsx`:
  - Server loader: `GET /handlers` → handler aggregate list
  - Display per handler: name, replica count, events/min (last 5 min), p99 LLM latency, decision distribution bar chart
  - Link each handler card to `handlers/$name`
- `dashboard/app/routes/handlers.$name.tsx`:
  - Server loader: `GET /handlers/{name}` → handler detail; `GET /tasks?handler={name}&limit=20` → recent tasks
  - Sections: recent tasks list (links to task detail), decision distribution, DLQ count
- `dashboard/app/components/HandlerCard.tsx`: health card component for overview page

## Acceptance Criteria

- Handler health page shows correct replica counts and decision distribution for test data
- Handler detail shows recent tasks linked to their detail pages
- DLQ count shown correctly (0 if no DLQ messages)

## Key Files

- `dashboard/app/routes/handlers._index.tsx` (new)
- `dashboard/app/routes/handlers.$name.tsx` (new)
- `dashboard/app/components/HandlerCard.tsx` (new)
