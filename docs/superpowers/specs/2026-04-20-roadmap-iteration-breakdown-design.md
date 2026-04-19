# Roadmap Iteration Breakdown Design

**Date:** 2026-04-20  
**Status:** Approved  

## Problem

Phases 6–10 of the kape-io roadmap have been shipped as massive PRs (20–80 file changes each), making them difficult to review and internalize. The goal is to restructure the remaining pending phases into smaller, reviewable iterations — one logical concern per PR, targeting fewer than 20 file changes each.

## Decision

- Phases 1–5 (done) remain untouched as flat `.md` files.
- Each pending phase (6–10) becomes a subdirectory containing:
  - `README.md` — the current phase file content (goal, milestone gate, reference specs)
  - Numbered iteration files, one per logical concern

## Folder Structure

```
docs/roadmap/phases/
  01-crds-cel.md              ← done, untouched
  02-minimal-operator.md      ← done, untouched
  03-task-service.md          ← done, untouched
  04-minimal-runtime.md       ← done, untouched
  05-alertmanager-adapter.md  ← done, untouched

  06-full-operator/           ← already raised as PR, skipped here
    README.md
    ...

  07-full-runtime/
    README.md
    01-proxy-config.md
    02-kapeproxy-mcp-integration.md
    03-load-skill-tool.md
    04-memory-tool.md
    05-actions-router.md
    06-retry-dlq.md
    07-dedup-metrics.md

  08-audit-security/
    README.md
    01-k8s-audit-adapter.md
    02-network-policy.md
    03-prompt-injection-defense.md
    04-secret-management.md
    05-immutable-audit-log.md
    06-mtls-nats.md

  09-dashboard/
    README.md
    01-type-generation.md
    02-task-list-sse.md
    03-task-detail.md
    04-handler-routes.md
    05-auth-deploy.md

  10-helm-polish/
    README.md
    01-helm-infra-templates.md
    02-helm-app-templates.md
    03-examples.md
    04-docs-release.md
```

## Iteration Detail

### Phase 7 — Full Runtime (7 iterations)

| # | File | Scope | Est. files |
|---|------|-------|-----------|
| 1 | `01-proxy-config.md` | Update `config.py` for `[proxy]` section; remove per-tool sidecar config; update `settings.toml` schema | ~3 |
| 2 | `02-kapeproxy-mcp-integration.md` | Single `MCPToolkit` to kapeproxy; `call_tools` LangGraph node; full graph: `entry_router → reason ⇄ call_tools → respond` | ~4 |
| 3 | `03-load-skill-tool.md` | `skills.py` `load_skill` tool; Jinja2 render context; graceful not-found; register in graph at startup | ~3 |
| 4 | `04-memory-tool.md` | Qdrant `QdrantVectorStore` retriever; register as LangChain tool in graph tool registry | ~3 |
| 5 | `05-actions-router.md` | `actions/router.py` + `event_emitter.py` + `save_memory.py` + `webhook.py`; JSONPath condition eval; partial failure logging | ~6 |
| 6 | `06-retry-dlq.md` | `tenacity` exponential backoff for LLM 429/503; non-retryable → DLQ publish to `kape.events.dlq.<handler-name>` | ~3 |
| 7 | `07-dedup-metrics.md` | In-memory dedup sliding window (60s TTL); Prometheus metrics (`kape_events_total`, `kape_llm_latency_seconds`, `kape_tool_calls_total`, `kape_decisions_total`) | ~4 |

### Phase 8 — Audit + Security (6 iterations)

| # | File | Scope | Est. files |
|---|------|-------|-----------|
| 1 | `01-k8s-audit-adapter.md` | Chi HTTP server at `adapters/kape-audit-adapter/`; K8s audit webhook → `kape.events.audit.<verb>.<resource>` CloudEvent | ~6 |
| 2 | `02-network-policy.md` | Handler pod egress NetworkPolicy; standard K8s + Cilium variants in `examples/networkpolicy/` | ~4 |
| 3 | `03-prompt-injection-defense.md` | `runtime/graph/system_prompt.j2`; HTML-escape `event_raw` fields; XML `<event>` tag isolation | ~3 |
| 4 | `04-secret-management.md` | ESO `SecretStore` + `ExternalSecret` example manifests; operator mounts KapeTool Secrets as files not env vars | ~5 |
| 5 | `05-immutable-audit-log.md` | PostgreSQL `kape_writer` INSERT-only role; trigger blocking UPDATE on terminal-state rows | ~3 |
| 6 | `06-mtls-nats.md` | cert-manager `Certificate` resources for NATS + all clients; update NATS StatefulSet + publisher/consumer configs | ~6 |

### Phase 9 — Dashboard (5 iterations)

| # | File | Scope | Est. files |
|---|------|-------|-----------|
| 1 | `01-type-generation.md` | Generate TypeScript types from `task-service/openapi/openapi.yaml` → `dashboard/app/types/api.ts`; codegen tooling | ~3 |
| 2 | `02-task-list-sse.md` | `tasks._index.tsx` — live task feed; `EventSource` on `GET /tasks/stream`; infinite scroll | ~5 |
| 3 | `03-task-detail.md` | `tasks.$id.tsx` — event payload, LLM decision, action timeline, Arize trace link | ~4 |
| 4 | `04-handler-routes.md` | `handlers._index.tsx` (health overview) + `handlers.$name.tsx` (detail: recent tasks, DLQ count) | ~6 |
| 5 | `05-auth-deploy.md` | OAuth2 Proxy GitHub org/team check; Dockerfile; Ingress with TLS; Deployment + ClusterIP Service | ~6 |

### Phase 10 — Helm + Polish (4 iterations)

| # | File | Scope | Est. files |
|---|------|-------|-----------|
| 1 | `01-helm-infra-templates.md` | Helm templates: NATS JetStream StatefulSet, CloudNativePG cluster, cert-manager Issuer + Certificate, CRDs | ~10 |
| 2 | `02-helm-app-templates.md` | Helm templates: operator, task-service, adapters, dashboard, OAuth2 Proxy; `values.yaml` with all defaults | ~12 |
| 3 | `03-examples.md` | `examples/alertmanager-handler/` + `examples/audit-handler/` — complete CRD manifests for each use case | ~8 |
| 4 | `04-docs-release.md` | `DEMO.md` runbook; `CHANGELOG.md` per component; `0.1.0` tag | ~4 |

## Success Criteria

- Each iteration file maps to exactly one PR
- Each PR targets fewer than 20 file changes
- Each iteration file contains: goal, work items, acceptance criteria, key files
- Phase `README.md` files retain the milestone gate and reference specs
