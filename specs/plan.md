# KAPE — Discussion Plan

## How to Use This Document

This document is a structured session-by-session plan for building KAPE through design discussions with Claude. Each session has a clear goal, the context needed to start, the questions to work through, and the expected output.

**How each session works:**

1. Open a new Claude conversation
2. Paste the "Session Starter" block at the top of your first message
3. Attach the referenced documents
4. Work through the discussion questions in order
5. End the session by generating the output document listed

**Current state:** Session 1–2 complete (this document is the output of those sessions).

---

## What Has Been Decided (Do Not Re-Discuss)

Before starting any session, Claude must treat the following as settled design decisions:

### Platform identity

- Name: **KAPE** (Kubernetes Agentic Platform Execution)
- API group: `kape.io/v1alpha1`
- Namespace: `kape-system`

### CRD names and responsibilities

- `KapeHandler` — one complete agent pipeline per CRD
- `KapeTool` — tool capability registration (types: `mcp`, `memory`, `event-publish`)
- `KapeSchema` — structured output contract for LLM decisions
- `KapePolicy` — (v2) cross-handler guardrails

### Core design principles

- Engineers write **intent** (prompts, guardrails, conditions) — not infrastructure wiring
- **No `context` section** — agent self-enriches by calling MCP tools during ReAct loop
- **`event-publish` tools live in `actions[]` only** — LLM fills `$prompt` content fields, engineer controls routing via conditions
- **Memory isolation boundary = KapeTool instance** — handlers sharing a KapeTool share a vector DB collection
- **MCP server lifecycle = engineer's responsibility** — KAPE only consumes MCP endpoints
- `allow`/`deny` on `mcp` KapeTool filters tool registry at pod startup — LLM never sees filtered tools

### Implementation stack

- Handler runtime: **Python + LangGraph** (two-phase: ReAct loop → structured output)
- LLM SDK: **LangChain** (`bind_tools` for Phase 1, `with_structured_output` for Phase 2)
- MCP adapter: **langchain-mcp-adapters** (MCPToolkit)
- Memory backend: **Qdrant** (primary), pgvector / Weaviate (alternatives)
- Event broker: **NATS JetStream** (recommended, not finalised)
- Scaling: **KEDA** (NatsJetStream scaler, one ScaledObject per KapeHandler)
- Observability: **OTEL → Arize OpenInference**, Prometheus metrics, UI Dashboard

### Resolved open questions

- Schema versioning: `spec.version` field on `KapeSchema`, new name for breaking changes
- Template language: **Jinja2** for `userPrompt` and `{{ }}` fields
- Handler observability metrics: defined in RFC section 8

---

## Session Index

| Session | Topic                                                         | Status  | Output                             |
| ------- | ------------------------------------------------------------- | ------- | ---------------------------------- |
| 1       | Platform naming, CRD philosophy, KapeHandler design           | ✅ Done | `kape-crd-rfc.md`                  |
| 2       | KapeTool type system, event-publish design, security layering | ✅ Done | `kape-rfc.md`                      |
| 3       | Open questions resolution                                     | ✅ Done | Updated RFC open questions section |
| 4       | Handler runtime technical design                              | ✅ Done | `kape-handler-runtime-design.md`   |
| 5       | Kape Operator technical design                                | ✅ Done | `kape-operator-design.md`          |
| 6       | Event broker and CloudEvents adapter design                   | ✅ Done | `kape-event-broker-design.md`      |
| 7       | Security hardening deep dive                                  | ✅ Done | `kape-security-design.md`          |
| 8       | Audit DB and Task record schema                               | ✅ Done | `kape-audit-design.md`             |
| 9       | UI Dashboard design                                           | ✅ Done | `kape-dashboard-design.md`         |
| 10      | CRD CEL validation rules                                      | ✅ Done | `kape-cel-validation.md`           |
| 11      | Repository structure and project layout                       | ✅ Done | `kape-repo-structure.md`           |
| 12      | v1 implementation roadmap                                     | ⬜      | `kape-v1-roadmap.md`               |
| 13      | v2 design: Argo, KapePolicy, KapeConfig                       | ⬜      | `kape-v2-design.md`                |

---

## Session 3 — Open Questions Resolution

**Goal:** Resolve the 8 open questions in the RFC so the design is complete before implementation work begins.

**Attach:**

- `kape-rfc.md`
- `kape-crd-rfc.md`

**Session starter:**

```
I am building KAPE (Kubernetes Agentic Platform Execution) — a Kubernetes-native,
event-driven AI agent platform. The design is captured in the attached RFC and CRD
design documents. All settled design decisions are in the documents — please treat
them as final.

Today's goal: work through the 8 open questions in RFC section 10, one by one,
to reach a decision on each. Start with question 1.
```

**Questions to work through (from RFC section 10):**

1. **Multi-cluster support** — single operator with kubeconfig federation, or one operator per cluster with central aggregation? What does the CRD design look like for each option?

2. **LLM cost management** — per-handler token budgets vs shared quota vs cost attribution per namespace. Where does this live — `KapeHandler`, `KapeConfig`, or both?

3. **Handler warm-up and replay** — should `trigger.replayOnStartup` and `trigger.maxEventAgeSeconds` be added to `KapeHandler`? What is the default behaviour?

4. **Approval workflow UX** — confirm deferral to v2. Define the interface contract now so v2 design is not blocked: what does the `KapeApproval` CRD look like structurally?

5. **MCP server discovery** — manual `KapeTool` authoring vs sidecar auto-registration. Decide default for v1, document the auto-registration pattern for v2.

6. **KapeConfig CRD** — define the full field set now even if implementation is v2. Fields: embedding model + dimensions, default LLM provider, global dry-run flag, audit DB connection string reference, token budget.

7. **KEDA threshold auto-tuning** — manual thresholds for v1. Define the auto-tuning algorithm for v2: what metric does the operator observe, what is the adjustment formula?

8. **Handler pod resource requests** — decide default CPU/memory requests for handler pods. Rationale: LLM calls are I/O-bound, not CPU-bound. Propose a starting point and how it interacts with KEDA.

**Expected output:** Updated RFC section 10 with all 8 questions resolved and decisions recorded.

---

## Session 4 — Handler Runtime Technical Design

**Goal:** Produce a complete technical design for the Kape Handler Runtime — the Python process running inside each handler pod. This is the most critical implementation document.

**Attach:**

- `kape-rfc.md`
- `kape-crd-rfc.md`

**Session starter:**

```
I am building KAPE (Kubernetes Agentic Platform Execution). The RFC and CRD design
are attached as settled reference. All design decisions in those documents are final.

Today's goal: design the Kape Handler Runtime in detail — the Python process that
runs inside each handler pod. This is a LangGraph-based ReAct agent. We need to
design every layer: startup, tool registry, NATS consumer loop, LangGraph graph
structure, structured output, ActionsRouter, Task record persistence, OTEL tracing,
and error handling. Work through each layer interactively with me before writing
the final design document.
```

**Layers to design (work through each interactively):**

**Layer 1 — Startup and CRD loading**

- How does the pod know which `KapeHandler` it serves? (env var injection by operator)
- How does it load its own CRD spec at startup?
- What happens if the CRD is not found or invalid at startup?
- How does it watch for CRD updates (hot reload)?

**Layer 2 — Tool registry construction**

- How are `KapeTool` CRDs fetched and validated at startup?
- How is `MCPToolkit` initialised per `mcp` KapeTool?
- How is the `allow`/`deny` filter applied at registration time?
- How is the LangChain `VectorStore` retriever constructed per `memory` KapeTool?
- How are `event-publish` KapeTools registered separately in the ActionsRouter?
- What happens if an MCP server is unreachable at startup?

**Layer 3 — NATS consumer loop**

- Consumer group naming convention
- Dedup window implementation (in-memory sliding window)
- Message acknowledgement strategy (ack after Task persisted, not after LLM call)
- Backpressure handling when LLM is slow

**Layer 4 — LangGraph graph structure**

- Node: `prompt_builder` — renders `userPrompt` Jinja2 template with `{{ event }}`
- Node: `agent` — ReAct loop with `bind_tools(mcp_and_memory_tools)`
- Node: `tools` — `ToolNode` from LangGraph
- Node: `respond` — structured output with `with_structured_output(MergedPydanticModel)`
- Edge: `tools_condition` — routes agent → tools or agent → respond
- How is the merged Pydantic model built from `KapeSchema` + `$prompt` fields?

**Layer 5 — ActionsRouter**

- JSONPath condition evaluation against decision object
- `emit` action: resolve `{{ }}` fields, extract `$prompt` field values from decision, publish CloudEvent
- `persist` action: write Task record to audit DB
- Execution order and partial failure handling

**Layer 6 — Task record lifecycle**

- Task created with `status: pending` before NATS ack
- Updated to `status: running` before LangGraph invocation
- Updated to `status: completed / failed / low-confidence` after ActionsRouter
- Schema: all fields from RFC section 7 Layer 7

**Layer 7 — OTEL tracing**

- Span structure for LangGraph nodes
- Arize OpenInference semantic conventions for LLM spans
- Tool call spans (input, output, latency)
- How `otel_trace_id` is linked to the Task record

**Layer 8 — Retry and DLQ**

- Exponential backoff implementation using `tenacity`
- Which errors trigger retry vs immediate DLQ
- DLQ message format (original CloudEvent + error context)

**Layer 9 — Prometheus metrics**

- Which library (`prometheus_client`)
- How metrics are registered and exposed
- Metric labels (handler name, tool name, decision value, model name)

**Expected output:** `kape-handler-runtime-design.md` — complete technical design with code structure, class diagram, and sequence diagram for the full execution flow.

---

## Session 5 — Kape Operator Technical Design

**Goal:** Design the Kape Operator — the Kubernetes controller that manages the lifecycle of all KAPE resources.

**Attach:**

- `kape-rfc.md`
- `kape-crd-rfc.md`
- `kape-handler-runtime-design.md`

**Session starter:**

```
I am building KAPE. The attached documents capture the settled design. Today's goal:
design the Kape Operator in detail — the Kubernetes controller built with
controller-runtime that manages KapeHandler, KapeTool, and KapeSchema CRDs.
Work through each reconciler interactively before writing the final design document.
```

**Reconcilers to design:**

**KapeHandler reconciler**

- Watch: `KapeHandler` CRD
- On create/update: generate Handler Deployment spec from CRD, create/update Deployment, create/update KEDA ScaledObject, inject env vars (`KAPE_HANDLER_NAME`, `KAPE_NAMESPACE`, tool connection secrets)
- On delete: delete Deployment, delete ScaledObject, drain DLQ
- Status reconciliation: read Deployment pod status + metrics → write back to `KapeHandler.status`
- Hot reload: detect spec diff, patch Deployment (triggers rolling update)

**KapeTool reconciler (memory type)**

- Watch: `KapeTool` CRDs where `spec.type == memory`
- On create: provision vector DB (Qdrant collection), create connection Secret in `kape-system`, inject into all referencing handler Deployments
- On delete: confirm no handlers reference it, then delete vector DB collection and Secret
- How to detect which handlers reference a given KapeTool

**KapeSchema reconciler**

- Watch: `KapeSchema` CRDs
- Validate that `jsonSchema` is valid JSON Schema
- On delete: reject if any `KapeHandler` references this schema via `schemaRef`

**Leader election and HA**

- Lease-based leader election
- What happens to in-flight reconciliations during leader failover

**Expected output:** `kape-operator-design.md` — reconciler designs, CRD ownership model, RBAC manifest for the operator ServiceAccount, and leader election configuration.

---

## Session 6 — Event Broker and CloudEvents Adapter Design

**Goal:** Finalise the event broker decision and design the CloudEvents adapter layer that normalises producer outputs.

**Attach:**

- `kape-rfc.md`

**Session starter:**

```
I am building KAPE. The RFC is attached as settled reference. Today's goal:
finalise the event broker choice (NATS JetStream vs alternatives) and design
the CloudEvents adapter layer for each event producer. Work through each decision
interactively before writing the design document.
```

**Topics to work through:**

**Event broker finalisation**

- NATS JetStream vs Kafka vs Redis Streams — evaluate on: operational overhead in EKS, KEDA scaler maturity, exactly-once delivery, replay capability, message retention
- Decide and document the rationale
- NATS deployment model: single cluster or clustered? Persistence volume requirements.

**CloudEvents adapter design (per producer)**

- Falco: webhook output → CloudEvents adapter. What does the adapter Deployment look like? How is the Falco webhook configured to point to it?
- Kyverno: PolicyReport watcher → CloudEvents adapter. Watch `PolicyReport` CRDs, emit on new violations.
- Cilium: Hubble export → CloudEvents adapter. Hubble relay → adapter.
- K8s Audit: audit webhook → CloudEvents adapter. What audit policy selects the right events?
- Karpenter: which Karpenter events are available? Webhook or informer?
- Custom DaemonSet: design the DaemonSet spec for node-level signal collection.

**Topic structure finalisation**

- Confirm `kape.events.*` hierarchy
- Wildcard subscription patterns for handlers
- Retention policy per topic

**Expected output:** `kape-event-broker-design.md` — broker decision with rationale, adapter designs per producer, topic structure, and retention policy.

---

## Session 7 — Security Hardening Deep Dive

**Goal:** Harden the security model beyond the 7 layers in the RFC. Produce implementable security specifications.

**Attach:**

- `kape-rfc.md`
- `kape-crd-rfc.md`

**Session starter:**

```
I am building KAPE. The attached documents capture the settled design. Today's goal:
work through the security model in detail and produce implementable security
specifications for each layer. The RFC defines 7 layers — we need to go deeper
on each and identify any gaps.
```

**Topics to work through:**

**Layer 1 — MCP server RBAC templates**

- What is the minimum RBAC for a read-only k8s-mcp?
- What is the minimum RBAC for a write k8s-mcp?
- Should KAPE ship with recommended RBAC manifests for common MCP servers?

**Layer 2 — KapeTool allow/deny implementation**

- How is the allow list enforced if MCPToolkit doesn't support filtering natively?
- Post-registration filtering vs pre-registration discovery filtering

**Layer 3 — Prompt injection defence**

- What does the system prompt template look like in full?
- How is `html_escape` applied to event data?
- What additional defences against indirect prompt injection via tool results?

**Layer 4 — Network policy**

- Write the Cilium NetworkPolicy for handler pods
- Egress rules: NATS (4222), LLM provider (443), MCP servers (by label selector), audit DB (5432/8123)
- All other egress denied

**Layer 5 — CEL validation rules**

- Write the full CEL validation rules for `KapeHandler`, `KapeTool`, `KapeSchema`
- Fields to validate: confidence threshold, max replicas, endpoint format, allow/deny mutual exclusion, schema reference existence

**Layer 6 — Secret management**

- LLM API key: ESO + SecretStore pattern, rotation strategy
- MCP connection credentials: per-KapeTool Secret, injected by operator
- Audit DB credentials: mounted Secret, not env var
- Vector DB connection: Secret injected by operator on KapeTool provisioning

**Layer 7 — Audit log integrity**

- Append-only enforcement at DB level
- Who has read access to audit DB?
- Retention policy

**Expected output:** `kape-security-design.md` — complete security specifications with implementable manifests for each layer.

---

## Session 8 — Audit Database and Task Record Schema

**Goal:** Design the audit database schema, Task record lifecycle, and query patterns for the UI Dashboard.

**Attach:**

- `kape-rfc.md`

**Session starter:**

```
I am building KAPE. The RFC is attached. Today's goal: design the audit database
schema and Task record in detail. The RFC defines the Task record fields — we need
to decide the database technology, full schema with types and indexes, Task lifecycle
state machine, and query patterns the UI Dashboard will need.
```

**Key decisions made:**

- Database: **PostgreSQL via CloudNativePG** — mixed access patterns (point lookups + time-range scans + JSONB) make PostgreSQL the correct choice over ClickHouse
- `tool_audit_log`: **dropped** — MCP tool call detail owned by OTEL backend via OpenInference; `kape.task_id` span attribute enables cross-referencing
- `llm_prompt` / `llm_response`: **excluded** — owned by OTEL trace; doubles PII exposure with no query value
- `event_raw`: **added** as permanent immutable JSONB — required for retry re-publish flow
- Partitioning: **by month on `received_at`**
- Handler health aggregates: **Prometheus/OTEL backend** — not re-derived from PostgreSQL
- Dashboard elapsed time: **client-side** from `received_at` — no DB-layer computation

**Expected output:** `kape-audit-design.md` — database choice with rationale, full schema DDL, state machine, and access patterns.

---

## Session 9 — UI Dashboard Design

**Goal:** Design the UI Dashboard — the read-only monitoring interface for engineers watching agent execution.

**Attach:**

- `kape-rfc.md`
- `kape-audit-design.md`

**Session starter:**

```
I am building KAPE. The attached documents capture the settled design. Today's goal:
design the UI Dashboard in detail. The dashboard is read-only — it monitors agent
execution via Task records and OTEL traces. Work through the UX and technical design
interactively before producing the design document.
```

**Topics to work through:**

**Technology choice**

- React + REST API vs React + WebSocket for live feed
- Backend: Go or Python API server reading from audit DB
- Deployment: inside `kape-system` namespace, Ingress or port-forward only?

**Page designs**

- Live Task feed — columns, filters (by handler, status, time range), refresh rate
- Task detail view — full prompt, tool call timeline, decision, actions, link to Arize trace
- Handler health overview — per-handler cards: replica count, events/min, p99 latency, decision distribution pie
- DLQ monitor — failed Tasks with error context, manual replay button (v2)

**Real-time updates**

- WebSocket vs Server-Sent Events vs polling for live feed
- How does the frontend know when a new Task is written?

**Arize OpenInference integration**

- Is the trace link a direct deep link into Arize UI?
- Does the dashboard embed trace data inline or redirect?

**Expected output:** `kape-dashboard-design.md` — technology choice, page wireframes (described in text), API endpoints the frontend needs, and deployment spec.

---

## Session 10 — CRD CEL Validation Rules

**Goal:** Write the complete CEL validation rules for all KAPE CRDs, ready to embed in the CRD OpenAPI schema.

**Attach:**

- `kape-crd-rfc.md`
- `kape-security-design.md`

**Session starter:**

```
I am building KAPE. The attached documents capture the settled CRD design and
security requirements. Today's goal: write the complete CEL validation rules
for KapeHandler, KapeTool, and KapeSchema — ready to embed in the CRD manifests.
Work through each CRD field by field.
```

**Validation rules to write:**

**KapeHandler**

- `spec.llm.confidenceThreshold` ≥ 0.7 and ≤ 1.0
- `spec.llm.provider` must be one of: anthropic, openai, azure-openai, ollama
- `spec.scaling.maxReplicas` ≤ 50
- `spec.scaling.minReplicas` ≤ `spec.scaling.maxReplicas`
- `spec.retryPolicy.maxRetries` ≤ 10
- `spec.schemaRef` must not be empty
- Each `spec.tools[].ref` must be a valid DNS label

**KapeTool**

- `spec.type` must be one of: mcp, memory, event-publish
- If `spec.type == mcp`: `spec.mcp.endpoint` must be a cluster-internal URL (no external hostnames)
- If `spec.type == mcp`: `spec.mcp.allow` and `spec.mcp.deny` are mutually exclusive
- If `spec.type == memory`: `spec.memory.backend` must be one of: qdrant, pgvector, weaviate
- If `spec.type == memory`: `spec.memory.distanceMetric` must be one of: cosine, dot, euclidean
- If `spec.type == event-publish`: `spec.eventPublish.type` must follow `kape.events.*` format

**KapeSchema**

- `spec.version` must match pattern `v[0-9]+`
- `spec.jsonSchema` must be valid JSON Schema (validate `type: object` with `properties`)
- `required` fields must all appear in `properties`

**Expected output:** `kape-cel-validation.md` — complete CEL rules with test cases for valid and invalid inputs per rule.

---

## Session 11 — Repository Structure and Project Layout

**Goal:** Design the monorepo structure for KAPE — all components, their languages, and how they relate.

**Attach:**

- `kape-rfc.md`
- `kape-operator-design.md`
- `kape-handler-runtime-design.md`

**Session starter:**

```
I am building KAPE. The attached documents describe the components. Today's goal:
design the repository structure — where each component lives, what language it's
written in, how the build system works, and how CI/CD is structured.
```

**Components to place:**

- `kape-operator` — Go, controller-runtime
- `kape-runtime` — Python, LangGraph, the handler pod process
- `kape-crds` — YAML manifests for all CRDs with CEL validation
- `kape-dashboard` — React frontend + Go/Python API backend
- `kape-adapters` — CloudEvents adapters per producer (Falco, Kyverno, etc.)
- `kape-helm` — Helm chart for full KAPE installation
- `kape-docs` — RFC and design documents
- `kape-examples` — Sample KapeHandler, KapeTool, KapeSchema for common use cases

**Topics to work through:**

- Monorepo vs multi-repo — rationale
- Go module structure for operator
- Python packaging for runtime (uv? poetry?)
- CRD generation: `controller-gen` from Go types or hand-authored YAML?
- Docker image strategy: one image per component or shared base?
- CI pipeline: lint → test → build → push → deploy to dev cluster
- Release strategy: semantic versioning, Helm chart versioning

**Expected output:** `kape-repo-structure.md` — full directory tree, language decisions, build system, CI pipeline design.

---

## Session 12 — v1 Implementation Roadmap

**Goal:** Produce a concrete implementation roadmap for v1 — what gets built, in what order, with what milestones.

**Attach:**

- All design documents produced in sessions 3–11

**Session starter:**

```
I am building KAPE. All design documents are attached. Today's goal: produce a
concrete v1 implementation roadmap. v1 scope is defined in the RFC. Work through
the build order, dependencies between components, and milestone definitions
interactively before producing the roadmap document.
```

**Topics to work through:**

**v1 scope confirmation**

- What is the minimum v1 that demonstrates the full agent loop end-to-end?
- Which event producers are in scope for v1? (suggest: Karpenter + Falco as the two reference producers)
- Which MCP servers ship as examples? (suggest: k8s-mcp + grafana-mcp)
- Is the UI Dashboard in v1 or v2?

**Build order (dependency graph)**

- Phase 1 — Foundation: CRDs + operator skeleton + NATS deployment + handler runtime skeleton
- Phase 2 — Agent loop: LangGraph integration + tool registry + NATS consumer + structured output
- Phase 3 — Actions: ActionsRouter + event-publish tool + Task record persistence
- Phase 4 — Scaling: KEDA ScaledObject generation + dedup window + DLQ
- Phase 5 — Security: CEL validation + network policy + prompt injection defence
- Phase 6 — Observability: OTEL + Arize + Prometheus metrics + UI Dashboard
- Phase 7 — Packaging: Helm chart + example handlers + documentation

**Milestone definitions**

- M1: Single handler pod processes one event end-to-end (no scaling, no security)
- M2: Operator manages handler lifecycle from KapeHandler CRD
- M3: Handler-to-handler chaining works (Karpenter → GitOps example)
- M4: Full security model applied
- M5: Observability complete
- M6: v1 release candidate — Helm chart installable on fresh EKS cluster

**Expected output:** `kape-v1-roadmap.md` — phased build plan with milestones, dependency graph, and acceptance criteria per milestone.

---

## Session 13 — v2 Design: Argo, KapePolicy, KapeConfig

**Goal:** Design the v2 additions — human-in-the-loop approval, multi-step DAG remediation, KapePolicy, KapeConfig.

**Attach:**

- `kape-rfc.md`
- `kape-crd-rfc.md`
- `kape-v1-roadmap.md`

**Session starter:**

```
I am building KAPE. v1 is designed. Today's goal: design v2 additions — Argo
Workflows integration for human-in-the-loop and multi-step DAGs, the KapePolicy
CRD, and the KapeConfig CRD. Work through each interactively.
```

**Topics to work through:**

**KapeApprovalWorkflow (Argo)**

- What triggers an approval workflow? (handler emits to `kape.events.approvals.*`)
- What does the Argo `suspend` node look like?
- What is the approval UX? Slack interactive message? Dashboard action?
- What is the `KapeApproval` CRD pattern for expressing approval requirements?

**KapeRemediationWorkflow (Argo)**

- When is a multi-step DAG justified vs a simple `event-publish` chain?
- What does a `validate → snapshot → execute → verify → rollback` Argo template look like for KAPE?
- How does the `KapeRemediationWorkflow` CRD generate an Argo `WorkflowTemplate`?

**KapePolicy CRD**

- What constraints does it enforce? (which handlers can run in which namespaces, which tools can be used by which teams)
- How does the operator enforce it? (admission webhook? reconciler?)
- Example: platform team can use `k8s-mcp-write`, application teams cannot

**KapeConfig CRD**

- Full field set: embedding model + dimensions, default LLM provider, global dry-run flag, audit DB connection SecretRef, token budget per handler, token budget global
- Singleton pattern: only one `KapeConfig` allowed per cluster
- How are fields from `KapeConfig` injected into handler pods?

**Expected output:** `kape-v2-design.md` — complete v2 design with CRD schemas for `KapePolicy`, `KapeConfig`, `KapeApprovalWorkflow`, `KapeRemediationWorkflow`.

---

## Tips for Effective Sessions

**Starting a session:**

- Always attach the documents listed in the session's "Attach" section
- Paste the session starter verbatim — it sets the context efficiently
- If Claude re-opens settled decisions, redirect: "That's already decided — see the attached RFC section X"

**During a session:**

- Work one topic at a time — don't jump ahead
- When a design decision is reached, ask Claude to confirm it explicitly before moving on
- If a topic reveals a gap in earlier design, note it and address it at the end of the session
- Use Claude's simulate/role-play technique (as used in Session 2) when evaluating DX of a new design

**Ending a session:**

- Always ask Claude to produce the output document listed for that session
- Save the document immediately — it becomes an attachment for future sessions
- Update this discussion plan's session index table with ✅ Done

**If a session gets too long:**

- Split it — finish the current topic, generate a partial output document, start a new session with that document attached
- KAPE is complex enough that some sessions (especially 4 and 5) may need to be split into sub-sessions

---

## Document Registry

Keep this table updated as sessions complete:

| Document                         | Session produced | Description                        |
| -------------------------------- | ---------------- | ---------------------------------- |
| `kape-rfc.md`                    | 1–2              | Master RFC — full platform design  |
| `kape-crd-rfc.md`                | 1–2              | CRD schema reference               |
| `kape-discussion-plan.md`        | 2                | This document                      |
| `kape-handler-runtime-design.md` | 4                | Handler pod technical design       |
| `kape-operator-design.md`        | 5                | Kape Operator technical design     |
| `kape-event-broker-design.md`    | 6                | Event broker and adapter design    |
| `kape-security-design.md`        | 7                | Security hardening specifications  |
| `kape-audit-design.md`           | 8                | Audit DB schema and Task lifecycle |
| `kape-dashboard-design.md`       | 9                | UI Dashboard design                |
| `kape-cel-validation.md`         | 10               | CEL validation rules               |
| `kape-repo-structure.md`         | 11               | Repository structure               |
| `kape-v1-roadmap.md`             | 12               | v1 implementation roadmap          |
| `kape-v2-design.md`              | 13               | v2 design                          |
