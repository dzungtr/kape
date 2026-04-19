# Phase 9 — Dashboard

**Status:** pending
**Milestone:** M5
**Specs:** 0009, 0008
**Modified by:** 0012 (created)

## Goal

The read-only monitoring UI. Engineers can watch live task execution, inspect decisions, and monitor handler health without touching kubectl.

## Reference Specs

- `0009-dashboard-ui` — full dashboard design: route map, page wireframes, SSE integration, OAuth2 Proxy setup
- `0008-audit-db` — task and handler query patterns

## Work

- React Router v7 framework mode (TypeScript, server-side loaders)
- Generate TypeScript types from `task-service/openapi/openapi.yaml` → `dashboard/app/types/api.ts`
- Routes:
  - `tasks._index.tsx` — live task feed, SSE-connected, infinite scroll
  - `tasks.$id.tsx` — task detail: event payload, LLM decision, action timeline, Arize trace link
  - `handlers._index.tsx` — handler health overview: replica count, events/min, p99 latency, decision distribution
  - `handlers.$name.tsx` — handler detail: recent tasks, decision distribution, DLQ count
- SSE integration: `EventSource` connecting to `GET /tasks/stream`
- OAuth2 Proxy: GitHub org/team membership check
- Deployment: `kape-system` namespace, ClusterIP Service, Ingress with TLS termination
- Dockerfile: `node:22-alpine`, server-side rendering

## Acceptance Criteria

- Live task feed shows Tasks appearing in real time
- Task detail view shows event payload, structured decision, action results, Arize trace link
- Handler health page shows correct replica counts and decision distribution
- Unauthenticated request redirected to GitHub OAuth flow
- Non-member GitHub account denied access

**M5 gate:** Live task feed works; handler health cards visible; GitHub auth enforced.

## Key Files

- `dashboard/app/routes/`
- `dashboard/app/components/`
- `dashboard/Dockerfile`
