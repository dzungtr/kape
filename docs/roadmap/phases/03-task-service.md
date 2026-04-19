# Phase 3 — Task Service

**Status:** done
**Milestone:** —
**Specs:** 0008, 0009
**Modified by:** 0012 (created)

## Goal

Build the audit database API that the runtime will write Task records to, and that the dashboard will read from.

## Reference Specs

- `0008-audit-db` — `tasks` table DDL, JSONB schemas, state machine, indexes, partitioning strategy
- `0009-dashboard-ui` — API endpoints the dashboard requires
- `0004-kape-handler` — Task record fields and lifecycle state machine

## Work

- PostgreSQL schema: `tasks` table DDL from spec 0008
- CloudNativePG cluster manifest for production; `docker-compose.yml` with plain PostgreSQL for local dev
- Chi router with endpoints:
  - `POST /tasks` — create Task record
  - `PATCH /tasks/{id}/status` — update Task status
  - `GET /tasks/{id}` — fetch single Task
  - `GET /tasks` — list with filters (handler, status, time range, pagination)
  - `GET /tasks/stream` — SSE stream of new/updated Task events
  - `GET /handlers` — per-handler aggregates
- pgx connection pool, repository layer (`internal/repository/`)
- OpenAPI spec → generate TypeScript types for dashboard (`openapi/openapi.yaml`)
- Prometheus metrics: request count, latency histograms

## Acceptance Criteria

- `POST /tasks` creates a Task; `GET /tasks/{id}` returns it
- `PATCH /tasks/{id}/status` transitions `pending → running → completed`; invalid transitions rejected
- `GET /tasks/stream` delivers SSE events when Tasks are created or updated
- `GET /handlers` returns correct aggregates for test data

## Key Files

- `task-service/internal/api/`
- `task-service/internal/repository/`
- `task-service/internal/stream/`
- `task-service/openapi/openapi.yaml`
