> **Archived baseline.** This document is the original v1 roadmap as written in session 12.
> The live build sequence is maintained in [`docs/roadmap/`](../roadmap/phases.md).
> Refer there for current phase status, work items, and spec references.

---

# KAPE — v1 Implementation Roadmap

**Status:** Draft
**Author:** Dzung Tran
**Session:** 12 — v1 Implementation Roadmap
**Created:** 2026-04-03
**Depends on:** all specs 0001–0011

---

## Overview

This document is the concrete build plan for KAPE v1. All design decisions are settled in specs 0001–0011. This roadmap defines what gets built, in what order, and what success looks like at each milestone.

**Build strategy:** Thin vertical slice first, then broaden. The first priority is a working end-to-end agent loop (one event → one decision → one Task record). Everything else — full operator features, security hardening, dashboard, packaging — is built on top of that validated foundation.

**Builder:** Solo. All phases are strictly sequential.

**v1 scope:**

- Event producers: AlertManager, K8s Audit
- MCP tools: engineer-deployed (KAPE does not ship MCP servers)
- Memory tools: Qdrant (operator-provisioned)
- Dashboard: included in v1
- Delivery: `helm install` on a fresh cluster

**v2 deferred:** KapePolicy, human-in-the-loop approvals, Argo Workflows, multi-cluster, role-based dashboard access.

---

## Phase Overview

| Phase | Name                         | Key Output                              | Milestone |
| ----- | ---------------------------- | --------------------------------------- | --------- |
| 1     | CRDs + CEL validation        | CRD YAML + CEL rules applied to cluster | —         |
| 2     | Minimal operator             | KapeHandler → Deployment + ConfigMap    | —         |
| 3     | Task-service                 | Audit DB API + SSE stream               | —         |
| 4     | Minimal runtime              | NATS consumer → LangGraph → task write  | —         |
| 5     | AlertManager adapter         | First real event producer               | **M1**    |
| 6     | Full operator                | KapeTool, KapeSchema, KEDA              | **M2**    |
| 7     | Full runtime                 | MCP tools, memory, event-publish, DLQ   | **M3**    |
| 8     | K8s Audit adapter + security | Second producer + hardening             | **M4**    |
| 9     | Dashboard                    | Live task feed + handler health         | **M5**    |
| 10    | Helm + examples + polish     | v1 release candidate                    | **M6**    |

---

## Phase 1 — CRDs + CEL Validation

**Goal:** Establish the API contract that every other component reads or writes. Nothing else can be built correctly without the CRD types being final.

**Reference specs:**

- `0002-crds-design` — all CRD field schemas, types, and validation annotations
- `0010-CEL-rules` — complete CEL rules to embed as `x-kubernetes-validations` and the `ValidatingWebhookConfiguration` for cross-field rules

**Work:**

- Write Go struct types for `KapeHandler`, `KapeTool`, `KapeSchema` in `operator/infra/api/v1alpha1/`
- Annotate with controller-gen markers (`+kubebuilder:validation:*`, `+kubebuilder:object:root=true`)
- Run `make generate` → emit CRD YAML into `crds/`
- Embed all CEL validation rules from spec 0010 as `x-kubernetes-validations` in the CRD YAML
- Apply CRDs to a local kind cluster
- Write a `ValidatingWebhookConfiguration` for cross-field KapeHandler rules that cannot be expressed in CEL alone (per spec 0010)

**Acceptance criteria:**

- `kubectl apply -f crds/` succeeds on a fresh kind cluster
- `kubectl apply` of a KapeHandler with `confidenceThreshold: 0.5` is rejected with a clear CEL message
- `kubectl apply` of a KapeTool with both `allow` and `deny` set is rejected
- `kubectl apply` of a valid KapeHandler, KapeTool, KapeSchema all succeed

**Key files:**

- `operator/infra/api/v1alpha1/kapehandler_types.go`
- `operator/infra/api/v1alpha1/kapetool_types.go`
- `operator/infra/api/v1alpha1/kapeschema_types.go`
- `crds/` (generated)

---

## Phase 2 — Minimal Operator

**Goal:** The operator can watch a `KapeHandler` CRD and provision the handler Deployment with a correct `settings.toml` ConfigMap. This validates the operator→runtime configuration contract before the runtime is written.

**Reference specs:**

- `0002-crds-design` — KapeHandler field reference (spec fields to read and render into settings.toml)
- `0005-kape-operator` — KapeHandler reconciler design, Deployment builder, ConfigMap rendering, status conditions

**Work:**

- Implement `KapeHandler` reconciler in `operator/controller/handler.go`
- On create/update: render `settings.toml` ConfigMap from KapeHandler spec (LLM provider, model, confidence threshold, schema ref, tool refs, NATS subject, retry policy)
- On create/update: create/update handler Deployment (image, env vars: `KAPE_HANDLER_NAME`, `KAPE_NAMESPACE`, ConfigMap mount)
- On delete: delete Deployment and ConfigMap
- Status reconciliation: write `status.conditions` based on Deployment readiness
- Wire reconciler into `cmd/main.go` with `ff` config (kubeconfig, leader election, metrics addr)
- Run operator locally via `make run` against kind cluster

**Not in this phase:** KapeTool reconciler, sidecar injection, KEDA ScaledObject, webhook validation.

**Acceptance criteria:**

- Apply a KapeHandler → Deployment appears with correct image, env vars, and mounted settings.toml
- Update the KapeHandler's `spec.llm.model` → Deployment rolls with updated ConfigMap
- Delete the KapeHandler → Deployment and ConfigMap are removed
- `kubectl get kapehandler <name> -o yaml` shows `status.conditions` with `Ready: False` (pod not yet runnable)

**Key files:**

- `operator/controller/handler.go`
- `operator/reconcile/handler.go`
- `operator/cmd/main.go`
- `operator/infra/k8s/` (Deployment + ConfigMap builders)

---

## Phase 3 — Task-Service

**Goal:** Build the audit database API that the runtime will write Task records to, and that the dashboard will read from. Building it before the runtime means the persistence contract is tested from day one.

**Reference specs:**

- `0008-audit-db` — `tasks` table DDL, JSONB schemas, state machine, indexes, partitioning strategy
- `0009-dashboard-ui` — API endpoints the dashboard requires (informs the OpenAPI spec and SSE stream design)
- `0004-kape-handler` — Task record fields and lifecycle state machine the runtime will write

**Work:**

- PostgreSQL schema: `tasks` table DDL from spec 0008 (all columns, JSONB fields, indexes, monthly partitioning)
- CloudNativePG cluster manifest for production; `docker-compose.yml` with plain PostgreSQL for local dev
- Chi router with endpoints:
  - `POST /tasks` — create Task record
  - `PATCH /tasks/{id}/status` — update Task status
  - `GET /tasks/{id}` — fetch single Task
  - `GET /tasks` — list with filters (handler, status, time range, pagination)
  - `GET /tasks/stream` — SSE stream of new/updated Task events
  - `GET /handlers` — per-handler aggregates (event count, status distribution, p99 latency)
- pgx connection pool, repository layer (`internal/repository/`)
- OpenAPI spec → generate TypeScript types for dashboard (`openapi/openapi.yaml`)
- Prometheus metrics: request count, latency histograms

**Acceptance criteria:**

- `POST /tasks` creates a Task; `GET /tasks/{id}` returns it
- `PATCH /tasks/{id}/status` transitions `pending → running → completed`; invalid transitions are rejected (e.g. `completed → running`)
- `GET /tasks/stream` delivers SSE events when Tasks are created or updated
- `GET /handlers` returns correct aggregates for test data

**Key files:**

- `task-service/internal/api/`
- `task-service/internal/repository/`
- `task-service/internal/stream/`
- `task-service/openapi/openapi.yaml`

---

## Phase 4 — Minimal Runtime

**Goal:** A handler pod that connects to NATS, consumes a CloudEvent, runs the LangGraph agent (no MCP tools yet), produces a structured decision, and writes the full Task lifecycle to the task-service.

**Reference specs:**

- `0001-rfc` — overall agent loop design, event flow, two-phase execution model (ReAct → structured output)
- `0004-kape-handler` — 7-layer execution model, LangGraph graph structure, Task lifecycle state machine, OTEL span conventions, settings.toml schema

**Work:**

- `config.py`: dynaconf loader — reads `settings.toml` (mounted by operator) + env var overrides → typed `Config` dataclass
- `consumer.py`: NATS JetStream pull consumer loop — explicit ACK strategy (ACK after Task record created, before LLM call)
- `graph/graph.py`: minimal LangGraph graph — `entry_router → reason → respond` (no tool nodes yet)
  - `reason` node: LangChain LLM with `with_structured_output(MergedPydanticModel)`
  - `respond` node: validate confidence, build Task result
- `models.py`: `AgentState`, `Task`, `TaskStatus`, `ActionResult` Pydantic models
- `task_service.py`: httpx async client for task-service API (create, update status)
- `tracing.py`: OTEL setup with `openinference-instrumentation-langchain`, export to OTLP endpoint
- `probe.py`: FastAPI `/healthz` + `/readyz` (NATS connection check)
- Task lifecycle: `pending` (on NATS consume) → `running` (before LangGraph invoke) → `completed`/`failed`/`low-confidence` (after graph)

**Not in this phase:** MCP tool nodes, memory tools, event-publish, ActionsRouter, retry/DLQ, Prometheus metrics.

**Acceptance criteria:**

- Publish a test CloudEvent to NATS subject `kape.events.alertmanager` manually
- Handler pod (run locally with `python main.py`) picks up the event
- Task record in task-service DB transitions `pending → running → completed`
- `schema_output` JSONB contains the LLM's structured decision
- OTEL span visible in local Jaeger (or stdout exporter)

**Key files:**

- `runtime/config.py`
- `runtime/consumer.py`
- `runtime/graph/graph.py`, `runtime/graph/nodes.py`, `runtime/graph/state.py`
- `runtime/task_service.py`
- `runtime/tracing.py`
- `runtime/probe.py`

---

## Phase 5 — AlertManager Adapter

**Goal:** The first real event producer. AlertManager fires a webhook → adapter normalises it to a CloudEvent → publishes to NATS. Combined with Phase 4, this closes the M1 loop.

**Reference specs:**

- `0006-events-broker-design` — NATS stream topology, `KAPE_EVENTS` stream config, subject hierarchy, CloudEvent envelope schema, AlertManager adapter design

**Work:**

- HTTP server (Chi) at `adapters/kape-alertmanager-adapter/`
- Accept AlertManager webhook POST at `/webhook`
- Build CloudEvent from payload: `type = kape.events.alertmanager`, `source = alertmanager`, `subject` from `kape_subject` label (fallback: `kape.events.alertmanager.default`)
- Publish to NATS JetStream via shared `internal/nats/` publisher
- NATS JetStream setup: create `KAPE_EVENTS` stream with subject filter `kape.events.>`, 24h retention, R=3 (single node for dev, 3-node for prod)
- Local dev: NATS in docker-compose, single node, no mTLS
- Prometheus metrics: events received, events published, publish errors

**Acceptance criteria:**

- AlertManager (or `curl` to simulate) fires webhook → CloudEvent appears in NATS
- Handler pod (Phase 4) picks up the event → Task record written with `status: completed`
- `nats sub 'kape.events.>'` shows the CloudEvent

**→ M1 gate: Task record exists in DB with `status: completed` and populated `schema_output` driven by a real AlertManager alert.**

**Key files:**

- `adapters/kape-alertmanager-adapter/main.go`
- `adapters/internal/cloudevents/builder.go`
- `adapters/internal/nats/publisher.go`

---

## Phase 6 — Full Operator

**Goal:** The operator manages the full resource lifecycle — KapeTool (memory + mcp types), KapeSchema, and KEDA autoscaling. After this phase, a KapeHandler with tools configured deploys completely from a CRD apply.

**Reference specs:**

- `0002-crds-design` — KapeTool and KapeSchema field reference (memory, mcp, event-publish type specs)
- `0005-kape-operator` — KapeTool reconciler (Qdrant provisioning, sidecar injection), KapeSchema reconciler, KEDA ScaledObject generation, cross-resource watch design

**Work:**

- `KapeTool` reconciler — memory type:
  - Provision Qdrant StatefulSet + Service in `kape-system`
  - Create Qdrant collection via Qdrant HTTP API
  - Create connection Secret (`QDRANT_URL`, `QDRANT_COLLECTION`)
  - Inject Secret env vars into all referencing handler Deployments (rolling update)
  - On delete: confirm no handlers reference it; delete collection + StatefulSet + Secret
- `KapeTool` reconciler — mcp type:
  - Inject sidecar container (MCP proxy) into handler Deployment spec
  - Mount connection Secret as env vars in sidecar
  - Apply allow/deny filter — pass filtered tool list to sidecar via env var
- `KapeSchema` reconciler:
  - Validate `spec.jsonSchema` is a valid JSON Schema object with `properties`
  - Block deletion if any KapeHandler references this schema via `spec.schemaRef`
- KEDA ScaledObject generation in KapeHandler reconciler:
  - Create `ScaledObject` targeting handler Deployment
  - `NatsJetStreamScaler` on consumer group lag
  - `minReplicas`, `maxReplicas` from `spec.scaling`
- Cross-resource watch: KapeTool changes trigger KapeHandler reconciliation for all referencing handlers
- Leader election wired in `cmd/main.go`

**Acceptance criteria:**

- Apply KapeHandler + KapeTool (memory type) → Qdrant StatefulSet appears, handler Deployment has QDRANT\_\* env vars
- Apply KapeTool (mcp type) → sidecar injected into handler Deployment
- Apply KapeSchema → `kubectl get kapeschema` shows `status: Valid`
- Attempt to delete a KapeSchema referenced by a KapeHandler → deletion blocked with clear error
- KEDA ScaledObject visible; `kubectl get scaledobject` shows correct min/max replicas

**→ M2 gate: Full lifecycle from CRD apply to running handler with Qdrant and KEDA.**

**Key files:**

- `operator/controller/tool.go`
- `operator/controller/schema.go`
- `operator/reconcile/tool.go`
- `operator/reconcile/schema.go`
- `operator/infra/qdrant/`
- `operator/infra/k8s/scaledobject.go`

---

## Phase 7 — Full Runtime

**Goal:** The runtime gains the full agent capability: MCP tool calls, vector memory, event-publish for handler chaining, retry/DLQ, dedup window, and Prometheus metrics.

**Reference specs:**

- `0004-kape-handler` — MCP tool integration (MCPToolkit, allow/deny filter), ActionsRouter design, memory tool (Qdrant VectorStore), retry/DLQ strategy, Prometheus metric definitions
- `0006-events-broker-design` — event-publish CloudEvent format, subject routing for handler chaining

**Work:**

- MCP tool integration:
  - Connect to sidecar MCP proxy via SSE transport at startup
  - Build `MCPToolkit`, apply allow/deny filter, register tools with LangGraph
  - Add `call_tools` node (LangGraph `ToolNode`) to graph
  - Full graph: `entry_router → reason ⇄ call_tools → respond`
- Memory tool integration:
  - Connect to Qdrant via `QDRANT_URL` + `QDRANT_COLLECTION` env vars
  - Build LangChain `QdrantVectorStore` retriever
  - Register as a LangChain tool available during ReAct loop
- ActionsRouter (`actions/router.py`):
  - JSONPath condition evaluation against `schema_output`
  - `event-publish` action: resolve Jinja2 `{{ }}` fields, extract `$prompt` fields, publish CloudEvent to NATS
  - `webhook` action: HTTP POST to configured endpoint
  - `save_memory` action: Qdrant upsert
  - Execution order: all actions run; partial failures logged but do not fail the Task
- Retry/DLQ:
  - Wrap LangGraph invoke with `tenacity` exponential backoff (LLM transient errors: 429, 503)
  - Non-retryable errors (schema validation failure, CEL guardrail) → immediate DLQ
  - DLQ: publish to `kape.events.dlq.<handler-name>` with original CloudEvent + error context
- Dedup sliding window: in-memory set of CloudEvent IDs, 60s TTL, reject duplicates before NATS ACK
- Prometheus metrics: `prometheus_client` — `kape_events_total`, `kape_llm_latency_seconds`, `kape_tool_calls_total`, `kape_decisions_total` (by value), all labelled by handler name

**Acceptance criteria:**

- Handler calls an MCP tool during ReAct loop; tool call span visible in OTEL trace with OpenInference conventions
- Handler persists a memory entry to Qdrant; subsequent event retrieves it
- Handler A emits a CloudEvent via event-publish → Handler B picks it up; both Task records exist with correct `parent_task_id`
- Duplicate CloudEvent ID within 60s window is discarded (no second Task created)
- LLM 429 triggers retry with backoff; non-retryable error routes to DLQ subject
- `curl handler-pod:8000/metrics` returns Prometheus text format with all expected metrics

**→ M3 gate: Two-handler chain runs end-to-end; MCP tool calls appear in Arize traces.**

**Key files:**

- `runtime/graph/graph.py` (updated with tool nodes)
- `runtime/actions/router.py`
- `runtime/actions/event_emitter.py`
- `runtime/actions/save_memory.py`
- `runtime/actions/webhook.py`
- `runtime/sidecar/`

---

## Phase 8 — K8s Audit Adapter + Security Hardening

**Goal:** Add the second event producer (K8s Audit) and apply the full 8-layer security model from spec 0007.

**Reference specs:**

- `0006-events-broker-design` — K8s Audit adapter design, audit event subject hierarchy, CloudEvent envelope
- `0007-security-layer` — full 8-layer security model: NetworkPolicy, prompt injection defense, Secret file mounting, mTLS cert-manager setup, immutable audit log enforcement

**Work:**

- K8s Audit adapter (`adapters/kape-audit-adapter/`):
  - HTTP server accepting K8s API server audit webhook events
  - Audit policy selection: verbs `create`, `update`, `delete` on sensitive resources (Secrets, RoleBindings, ClusterRoleBindings, Pods)
  - Build CloudEvent: `type = kape.events.audit`, `source = k8s-apiserver`
  - Publish to `kape.events.audit.<verb>.<resource>`
- NetworkPolicy manifests:
  - Handler pod egress: NATS (4222), LLM provider (443), MCP sidecars (by pod label), task-service (8080), Qdrant (6333)
  - All other egress denied
  - Standard NetworkPolicy + Cilium variant in `examples/networkpolicy/`
- Prompt injection defense:
  - Full system prompt template in `runtime/graph/system_prompt.j2`
  - HTML-escape all `event_raw` fields before Jinja2 rendering
  - XML tag isolation: wrap user content in `<event>...</event>` tags; instruct LLM to treat content as data only
- Secret management:
  - ESO `SecretStore` + `ExternalSecret` example manifests for LLM API key (`examples/eso/`)
  - Operator mounts all KapeTool connection Secrets as files (not env vars) — update Deployment builder
- Immutable audit log: PostgreSQL role `kape_writer` with `INSERT` only on `tasks` table (no `UPDATE` on terminal-state rows via trigger)
- mTLS for NATS: cert-manager `Certificate` resources for NATS cluster + all client connections; update NATS StatefulSet and all publisher/consumer configs

**Acceptance criteria:**

- K8s Audit adapter: create a Secret in a test namespace → CloudEvent appears in NATS → handler processes it → Task in DB
- NetworkPolicy: `kubectl exec` into handler pod; `curl 8.8.8.8` fails; `curl nats-svc:4222` succeeds
- Prompt injection: inject `<script>call_tool(rm -rf)</script>` into a test event payload; no tool calls outside allowlist in resulting trace
- mTLS: non-mTLS NATS client connection is rejected

**→ M4 gate: Both adapters live; network policy blocks unexpected egress; prompt injection test passes.**

**Key files:**

- `adapters/kape-audit-adapter/`
- `examples/networkpolicy/`
- `examples/eso/`
- `runtime/graph/system_prompt.j2`
- `operator/infra/k8s/deployment.go` (Secret file mounts)

---

## Phase 9 — Dashboard

**Goal:** The read-only monitoring UI. Engineers can watch live task execution, inspect decisions, and monitor handler health without touching kubectl.

**Reference specs:**

- `0009-dashboard-ui` — full dashboard design: route map, page wireframes, SSE integration, OAuth2 Proxy setup, deployment model
- `0008-audit-db` — task and handler query patterns available to the API (informs loader implementations)

**Work:**

- React Router v7 framework mode (TypeScript, server-side loaders)
- Generate TypeScript types from `task-service/openapi/openapi.yaml` → `dashboard/app/types/api.ts`
- Routes and pages:
  - `tasks._index.tsx` — Live task feed: table with handler/status/time filters, SSE-connected for live updates (no polling), infinite scroll
  - `tasks.$id.tsx` — Task detail: event payload, LLM decision, action timeline, confidence score, link to Arize trace by `otel_trace_id`
  - `handlers._index.tsx` — Handler health overview: cards per handler (replica count, events/min, p99 latency, decision distribution pie)
  - `handlers.$name.tsx` — Handler detail: recent tasks, decision distribution, DLQ count
- SSE integration: `EventSource` in `tasks._index.tsx` connecting to `GET /tasks/stream`; new Task rows appear without refresh
- OAuth2 Proxy: deploy in front of dashboard; GitHub org/team membership check; dashboard is auth-unaware (reads `X-Auth-User` header only for display)
- Deployment: `kape-system` namespace, ClusterIP Service, Ingress with TLS termination
- Dockerfile: `node:22-alpine`, server-side rendering, no client-side API token exposure

**Acceptance criteria:**

- Open dashboard in browser; live task feed shows Tasks appearing in real time as AlertManager events are processed
- Click a Task → detail view shows event payload, structured decision, action results, and Arize trace link
- Handler health page shows correct replica counts and decision distribution from the last hour
- GitHub auth: unauthenticated request redirected to GitHub OAuth flow
- Non-member GitHub account denied access

**→ M5 gate: Live task feed works; handler health cards visible; GitHub auth enforced.**

**Key files:**

- `dashboard/app/routes/`
- `dashboard/app/components/`
- `dashboard/Dockerfile`

---

## Phase 10 — Helm + Examples + Polish

**Goal:** Package the full stack as a Helm chart installable on a fresh cluster. Ship example manifests. Write the demo runbook. v1 Release Candidate.

**Reference specs:**

- `0011-repo-structure` — Helm chart layout, image naming conventions, changeset-based release strategy
- All specs `0001–0010` — example manifests must accurately reflect the full CRD API and component configuration

**Work:**

- Helm chart (`helm/`) — templates for:
  - NATS JetStream StatefulSet (3-node, mTLS via cert-manager)
  - CloudNativePG cluster (3-node PostgreSQL)
  - KAPE operator Deployment + RBAC
  - task-service Deployment + Service + HorizontalPodAutoscaler
  - AlertManager adapter Deployment + Service
  - K8s Audit adapter Deployment + Service
  - Dashboard Deployment + Service + Ingress
  - OAuth2 Proxy Deployment + configuration
  - CRDs (via `crds/` directory hook)
  - cert-manager `Issuer` + `Certificate` for NATS mTLS
- `helm/values.yaml`: all configurable defaults (image tags, resource requests, LLM provider, NATS retention, replicas)
- `examples/alertmanager-handler/`: complete working KapeHandler + KapeTool + KapeSchema for AlertManager use case
- `examples/audit-handler/`: complete working example for K8s Audit use case
- `DEMO.md`: step-by-step runbook — install Helm chart, apply example manifests, fire a test AlertManager alert, watch task appear in dashboard
- Image tagging: all component images tagged `0.1.0` in `values.yaml`
- Changeset-based release: `npx changeset` → `CHANGELOG.md` per component → `0.1.0` tag

**Acceptance criteria:**

- `helm install kape ./helm --set llm.apiKeySecret=my-secret -n kape-system` on a fresh kind cluster: all pods reach `Running` within 5 minutes
- Apply `examples/alertmanager-handler/` manifests → handler pod starts
- `curl` test AlertManager payload to adapter → Task appears in dashboard within 30 seconds
- `helm uninstall kape` cleanly removes all resources (no orphaned PVCs or CRDs)
- `DEMO.md` runbook is self-contained and runnable by a new engineer

**→ M6 gate: Full install → real event → visible decision. v1 Release Candidate tagged.**

**Key files:**

- `helm/Chart.yaml`, `helm/values.yaml`, `helm/templates/`
- `examples/alertmanager-handler/`
- `examples/audit-handler/`
- `DEMO.md`

---

## Milestone Summary

| Milestone | After Phase | What It Proves                                                  |
| --------- | ----------- | --------------------------------------------------------------- |
| **M1**    | 5           | Core agent loop works: real alert → LLM decision → Task in DB   |
| **M2**    | 6           | Operator manages full lifecycle: KapeTool + KEDA from CRD apply |
| **M3**    | 7           | Handler chaining works: event-publish + MCP tools + memory      |
| **M4**    | 8           | Production-grade: both adapters, security hardened, mTLS        |
| **M5**    | 9           | Observable: live dashboard, Arize traces, GitHub auth           |
| **M6**    | 10          | Shippable: `helm install` on fresh cluster, demo runbook        |

---

## Sequencing Rationale

**Task-service before runtime (Phase 3 before 4):** The runtime's Task lifecycle calls the task-service HTTP API. Building the API first means integration testing persistence from day one, not stubbing it.

**Minimal operator before runtime (Phase 2 before 4):** The operator renders `settings.toml` into a ConfigMap that the runtime reads at startup. Validating this contract before writing the agent logic prevents late-discovered mismatches.

**Full operator after M1 (Phase 6 after Phase 5):** KapeTool reconciler and KEDA are only meaningful once a runtime consumer exists. Building them post-M1 enables immediate end-to-end validation of provisioning.

**K8s Audit adapter in Phase 8 (not Phase 5):** AlertManager has a simpler, flat webhook payload. Completing it first establishes the adapter pattern before tackling K8s Audit's more complex event schema and API server audit policy configuration.

**Dashboard after full runtime (Phase 9 after Phase 7):** The dashboard's value scales with the richness of task data. Launching it with the full data model (tool calls, retry lineage, action results) is more useful than launching it over minimal Phase 4 records.

**Helm last (Phase 10):** Helm packages a known-working system. Building chart templates in parallel with implementation creates drift between templates and actual component specs.

---

## Development Environment

**Local cluster:** kind (Kubernetes in Docker)

**Local services (docker-compose):**

- NATS (single node, no mTLS for dev)
- PostgreSQL (plain, no CloudNativePG for dev)
- Jaeger (OTEL trace viewer)

**Operator:** `make run` (runs outside cluster, reads `KUBECONFIG`)

**Runtime:** `python main.py` (runs outside cluster for development, reads settings.toml from local file)

**Adapters:** `go run ./cmd/kape-alertmanager-adapter` (local HTTP server, publishes to local NATS)

**Dashboard:** `npm run dev` (React Router dev server with HMR)

**Progression:** Phases 1–5 can be developed entirely with docker-compose + kind. Phase 6 onwards requires a running cluster (KEDA, Qdrant StatefulSet, cert-manager).

---

## Technical Backlog

Deferred implementation decisions that did not block their originating phase.

| Item | Deferred from | Target |
| ---- | ------------- | ------ |
| Add `storage` field to `KapeToolSpec.MemorySpec` so operators can configure Qdrant PVC size per memory tool. Phase 6 hardcodes 10Gi. | Phase 6 | Phase 6 follow-up PR |
