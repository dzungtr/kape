# Docs Restructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move root `specs/` under `docs/specs/` and introduce `docs/roadmap/` with a phases index and one file per build phase.

**Architecture:** Git-move all spec directories to preserve history, create the roadmap directory structure with content sourced from `specs/0012-v1-roadmap/README.md` split into per-phase files, and update phases 6 and 7 with additions from spec 0013.

**Tech Stack:** git mv, markdown

---

### Task 1: Move specs/ to docs/specs/

**Files:**
- Move: `specs/0001-rfc/` → `docs/specs/0001-rfc/`
- Move: `specs/0002-crds-design/` → `docs/specs/0002-crds-design/`
- Move: `specs/0003-q&a/` → `docs/specs/0003-q&a/`
- Move: `specs/0004-kape-handler/` → `docs/specs/0004-kape-handler/`
- Move: `specs/0005-kape-operator/` → `docs/specs/0005-kape-operator/`
- Move: `specs/0006-events-broker-design/` → `docs/specs/0006-events-broker-design/`
- Move: `specs/0007-security-layer/` → `docs/specs/0007-security-layer/`
- Move: `specs/0008-audit-db/` → `docs/specs/0008-audit-db/`
- Move: `specs/0009-dashboard-ui/` → `docs/specs/0009-dashboard-ui/`
- Move: `specs/0010-CEL-rules/` → `docs/specs/0010-CEL-rules/`
- Move: `specs/0011-repo-structure/` → `docs/specs/0011-repo-structure/`
- Move: `specs/0012-v1-roadmap/` → `docs/specs/0012-v1-roadmap/`
- Move: `specs/0013-kape-skill-crd/` → `docs/specs/0013-kape-skill-crd/`
- Move: `specs/plan.md` → `docs/specs/plan.md`

- [ ] **Step 1: Git-move all spec directories**

```bash
git mv specs/0001-rfc docs/specs/0001-rfc
git mv specs/0002-crds-design docs/specs/0002-crds-design
git mv "specs/0003-q&a" "docs/specs/0003-q&a"
git mv specs/0004-kape-handler docs/specs/0004-kape-handler
git mv specs/0005-kape-operator docs/specs/0005-kape-operator
git mv specs/0006-events-broker-design docs/specs/0006-events-broker-design
git mv specs/0007-security-layer docs/specs/0007-security-layer
git mv specs/0008-audit-db docs/specs/0008-audit-db
git mv specs/0009-dashboard-ui docs/specs/0009-dashboard-ui
git mv specs/0010-CEL-rules docs/specs/0010-CEL-rules
git mv specs/0011-repo-structure docs/specs/0011-repo-structure
git mv specs/0012-v1-roadmap docs/specs/0012-v1-roadmap
git mv specs/0013-kape-skill-crd docs/specs/0013-kape-skill-crd
git mv specs/plan.md docs/specs/plan.md
```

- [ ] **Step 2: Verify all files moved correctly**

```bash
ls docs/specs/
```

Expected output (13 directories + plan.md):
```
0001-rfc  0002-crds-design  0003-q&a  0004-kape-handler  0005-kape-operator
0006-events-broker-design  0007-security-layer  0008-audit-db  0009-dashboard-ui
0010-CEL-rules  0011-repo-structure  0012-v1-roadmap  0013-kape-skill-crd  plan.md
```

- [ ] **Step 3: Verify root specs/ is now empty (only contains the directory itself)**

```bash
ls specs/
```

Expected: empty output (no files remaining)

- [ ] **Step 4: Remove empty root specs/ directory**

```bash
rmdir specs/
```

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: move specs/ to docs/specs/ — consolidate all docs under docs/"
```

---

### Task 2: Add deprecation note to 0012 roadmap

**Files:**
- Modify: `docs/specs/0012-v1-roadmap/README.md` (prepend deprecation banner)

- [ ] **Step 1: Prepend deprecation notice to the top of the file**

Open `docs/specs/0012-v1-roadmap/README.md` and add this block as the very first lines, before the `# KAPE — v1 Implementation Roadmap` heading:

```markdown
> **Archived baseline.** This document is the original v1 roadmap as written in session 12.
> The live build sequence is maintained in [`docs/roadmap/`](../roadmap/phases.md).
> Refer there for current phase status, work items, and spec references.

---

```

- [ ] **Step 2: Verify the file starts with the notice**

```bash
head -6 docs/specs/0012-v1-roadmap/README.md
```

Expected:
```
> **Archived baseline.** This document is the original v1 roadmap as written in session 12.
> The live build sequence is maintained in [`docs/roadmap/`](../roadmap/phases.md).
> Refer there for current phase status, work items, and spec references.

---

```

- [ ] **Step 3: Commit**

```bash
git add docs/specs/0012-v1-roadmap/README.md
git commit -m "doc: mark 0012-v1-roadmap as archived baseline, point to docs/roadmap/"
```

---

### Task 3: Create docs/roadmap/phases.md

**Files:**
- Create: `docs/roadmap/phases.md`

- [ ] **Step 1: Create the roadmap directory and phases index**

```bash
mkdir -p docs/roadmap/phases
```

Create `docs/roadmap/phases.md` with this exact content:

```markdown
# KAPE Build Sequence

| Phase | Name | Status | Milestone | Specs | File |
|---|---|---|---|---|---|
| 1  | CRDs + CEL Validation    | done       | —  | 0002, 0010       | [phases/01-crds-cel.md](phases/01-crds-cel.md) |
| 2  | Minimal Operator         | done       | —  | 0002, 0005       | [phases/02-minimal-operator.md](phases/02-minimal-operator.md) |
| 3  | Task Service             | done       | —  | 0008, 0009       | [phases/03-task-service.md](phases/03-task-service.md) |
| 4  | Minimal Runtime          | done       | —  | 0001, 0004       | [phases/04-minimal-runtime.md](phases/04-minimal-runtime.md) |
| 5  | AlertManager Adapter     | done       | M1 | 0006             | [phases/05-alertmanager-adapter.md](phases/05-alertmanager-adapter.md) |
| 6  | Full Operator            | pending    | M2 | 0002, 0005, 0013 | [phases/06-full-operator.md](phases/06-full-operator.md) |
| 7  | Full Runtime             | pending    | M3 | 0004, 0006, 0013 | [phases/07-full-runtime.md](phases/07-full-runtime.md) |
| 8  | K8s Audit + Security     | pending    | M4 | 0006, 0007       | [phases/08-audit-security.md](phases/08-audit-security.md) |
| 9  | Dashboard                | pending    | M5 | 0009, 0008       | [phases/09-dashboard.md](phases/09-dashboard.md) |
| 10 | Helm + Examples + Polish | pending    | M6 | 0011             | [phases/10-helm-polish.md](phases/10-helm-polish.md) |
```

Status values: `done` | `in-progress` | `pending`

- [ ] **Step 2: Verify file created**

```bash
cat docs/roadmap/phases.md
```

Expected: the table above with 10 phase rows.

- [ ] **Step 3: Commit**

```bash
git add docs/roadmap/phases.md
git commit -m "doc: add docs/roadmap/phases.md — live build sequence index"
```

---

### Task 4: Create phase files 01–05 (done phases)

**Files:**
- Create: `docs/roadmap/phases/01-crds-cel.md`
- Create: `docs/roadmap/phases/02-minimal-operator.md`
- Create: `docs/roadmap/phases/03-task-service.md`
- Create: `docs/roadmap/phases/04-minimal-runtime.md`
- Create: `docs/roadmap/phases/05-alertmanager-adapter.md`

- [ ] **Step 1: Create 01-crds-cel.md**

```markdown
# Phase 1 — CRDs + CEL Validation

**Status:** done
**Milestone:** —
**Specs:** 0002, 0010
**Modified by:** 0012 (created), 0013 (KapeSkill CRD types + spec.skills[] field added)

## Goal

Establish the API contract that every other component reads or writes. Nothing else can be built correctly without the CRD types being final.

## Reference Specs

- `0002-crds-design` — all CRD field schemas, types, and validation annotations
- `0010-CEL-rules` — complete CEL rules to embed as `x-kubernetes-validations` and the `ValidatingWebhookConfiguration` for cross-field rules
- `0013-kape-skill-crd` — KapeSkill CRD types, KapeHandler spec.skills[] field

## Work

- Write Go struct types for `KapeHandler`, `KapeTool`, `KapeSchema`, `KapeSkill` in `operator/infra/api/v1alpha1/`
- Annotate with controller-gen markers (`+kubebuilder:validation:*`, `+kubebuilder:object:root=true`)
- Add `spec.skills[]` field to KapeHandler type (list of skill refs)
- Run `make generate` → emit CRD YAML into `crds/`
- Embed all CEL validation rules from spec 0010 as `x-kubernetes-validations` in the CRD YAML
- Apply CRDs to a local kind cluster
- Write a `ValidatingWebhookConfiguration` for cross-field KapeHandler rules that cannot be expressed in CEL alone (per spec 0010)

## Acceptance Criteria

- `kubectl apply -f crds/` succeeds on a fresh kind cluster
- `kubectl apply` of a KapeHandler with `confidenceThreshold: 0.5` is rejected with a clear CEL message
- `kubectl apply` of a KapeTool with both `allow` and `deny` set is rejected
- `kubectl apply` of a valid KapeHandler, KapeTool, KapeSchema, KapeSkill all succeed

## Key Files

- `operator/infra/api/v1alpha1/kapehandler_types.go`
- `operator/infra/api/v1alpha1/kapetool_types.go`
- `operator/infra/api/v1alpha1/kapeschema_types.go`
- `operator/infra/api/v1alpha1/kapeskill_types.go`
- `crds/` (generated)
```

- [ ] **Step 2: Create 02-minimal-operator.md**

```markdown
# Phase 2 — Minimal Operator

**Status:** done
**Milestone:** —
**Specs:** 0002, 0005
**Modified by:** 0012 (created)

## Goal

The operator can watch a `KapeHandler` CRD and provision the handler Deployment with a correct `settings.toml` ConfigMap. This validates the operator→runtime configuration contract before the runtime is written.

## Reference Specs

- `0002-crds-design` — KapeHandler field reference
- `0005-kape-operator` — KapeHandler reconciler design, Deployment builder, ConfigMap rendering, status conditions

## Work

- Implement `KapeHandler` reconciler in `operator/controller/handler.go`
- On create/update: render `settings.toml` ConfigMap from KapeHandler spec
- On create/update: create/update handler Deployment (image, env vars, ConfigMap mount)
- On delete: delete Deployment and ConfigMap
- Status reconciliation: write `status.conditions` based on Deployment readiness
- Wire reconciler into `cmd/main.go` with `ff` config (kubeconfig, leader election, metrics addr)
- Run operator locally via `make run` against kind cluster

**Not in this phase:** KapeTool reconciler, sidecar injection, KEDA ScaledObject, webhook validation.

## Acceptance Criteria

- Apply a KapeHandler → Deployment appears with correct image, env vars, and mounted settings.toml
- Update the KapeHandler's `spec.llm.model` → Deployment rolls with updated ConfigMap
- Delete the KapeHandler → Deployment and ConfigMap are removed
- `kubectl get kapehandler <name> -o yaml` shows `status.conditions` with `Ready: False`

## Key Files

- `operator/controller/handler.go`
- `operator/reconcile/handler.go`
- `operator/cmd/main.go`
- `operator/infra/k8s/`
```

- [ ] **Step 3: Create 03-task-service.md**

```markdown
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
```

- [ ] **Step 4: Create 04-minimal-runtime.md**

```markdown
# Phase 4 — Minimal Runtime

**Status:** done
**Milestone:** —
**Specs:** 0001, 0004
**Modified by:** 0012 (created)

## Goal

A handler pod that connects to NATS, consumes a CloudEvent, runs the LangGraph agent (no MCP tools yet), produces a structured decision, and writes the full Task lifecycle to the task-service.

## Reference Specs

- `0001-rfc` — overall agent loop design, event flow, two-phase execution model
- `0004-kape-handler` — 7-layer execution model, LangGraph graph structure, Task lifecycle, OTEL spans, settings.toml schema

## Work

- `config.py`: dynaconf loader — reads `settings.toml` + env var overrides → typed `Config` dataclass
- `consumer.py`: NATS JetStream pull consumer loop — explicit ACK strategy
- `graph/graph.py`: minimal LangGraph graph — `entry_router → reason → respond`
- `models.py`: `AgentState`, `Task`, `TaskStatus`, `ActionResult` Pydantic models
- `task_service.py`: httpx async client for task-service API
- `tracing.py`: OTEL setup with `openinference-instrumentation-langchain`
- `probe.py`: FastAPI `/healthz` + `/readyz`
- Task lifecycle: `pending → running → completed/failed/low-confidence`

**Not in this phase:** MCP tool nodes, memory tools, event-publish, ActionsRouter, retry/DLQ, Prometheus metrics.

## Acceptance Criteria

- Publish a test CloudEvent to NATS manually
- Handler pod picks up the event
- Task record transitions `pending → running → completed`
- `schema_output` JSONB contains the LLM's structured decision
- OTEL span visible in local Jaeger

## Key Files

- `runtime/config.py`
- `runtime/consumer.py`
- `runtime/graph/graph.py`, `runtime/graph/nodes.py`, `runtime/graph/state.py`
- `runtime/task_service.py`
- `runtime/tracing.py`
- `runtime/probe.py`
```

- [ ] **Step 5: Create 05-alertmanager-adapter.md**

```markdown
# Phase 5 — AlertManager Adapter

**Status:** done
**Milestone:** M1
**Specs:** 0006
**Modified by:** 0012 (created)

## Goal

The first real event producer. AlertManager fires a webhook → adapter normalises it to a CloudEvent → publishes to NATS. Closes the M1 loop with Phase 4.

## Reference Specs

- `0006-events-broker-design` — NATS stream topology, CloudEvent envelope schema, AlertManager adapter design

## Work

- HTTP server (Chi) at `adapters/kape-alertmanager-adapter/`
- Accept AlertManager webhook POST at `/webhook`
- Build CloudEvent: `type = kape.events.alertmanager`, `source = alertmanager`
- Publish to NATS JetStream via shared `internal/nats/` publisher
- NATS JetStream setup: create `KAPE_EVENTS` stream
- Prometheus metrics: events received, events published, publish errors

## Acceptance Criteria

- AlertManager (or `curl`) fires webhook → CloudEvent appears in NATS
- Handler pod picks up the event → Task record written with `status: completed`
- `nats sub 'kape.events.>'` shows the CloudEvent

**M1 gate:** Task record exists in DB with `status: completed` and populated `schema_output` driven by a real AlertManager alert.

## Key Files

- `adapters/kape-alertmanager-adapter/main.go`
- `adapters/internal/cloudevents/builder.go`
- `adapters/internal/nats/publisher.go`
```

- [ ] **Step 6: Verify all five files created**

```bash
ls docs/roadmap/phases/
```

Expected:
```
01-crds-cel.md  02-minimal-operator.md  03-task-service.md
04-minimal-runtime.md  05-alertmanager-adapter.md
```

- [ ] **Step 7: Commit**

```bash
git add docs/roadmap/phases/
git commit -m "doc: add roadmap phase files 01-05 (done phases)"
```

---

### Task 5: Create phase file 06 — Full Operator (updated with 0013)

**Files:**
- Create: `docs/roadmap/phases/06-full-operator.md`

- [ ] **Step 1: Create 06-full-operator.md**

```markdown
# Phase 6 — Full Operator

**Status:** pending
**Milestone:** M2
**Specs:** 0002, 0005, 0013
**Modified by:** 0012 (created), 0013 (KapeSkillReconciler, KapeProxyReconciler, kapeproxy sidecar model added)

## Goal

The operator manages the full resource lifecycle — KapeTool (memory + mcp types), KapeSchema, KapeSkill, KapeProxy sidecar injection, and KEDA autoscaling. After this phase, a KapeHandler with tools and skills configured deploys completely from a CRD apply.

## Reference Specs

- `0002-crds-design` — KapeTool and KapeSchema field reference
- `0005-kape-operator` — KapeTool reconciler, KapeSchema reconciler, KEDA ScaledObject generation
- `0013-kape-skill-crd` — KapeSkillReconciler, KapeHandlerReconciler changes, kapeproxy sidecar model, kapeproxy-config rendering

## Work

### KapeTool reconciler — memory type
- Provision Qdrant StatefulSet + Service in `kape-system`
- Create Qdrant collection via Qdrant HTTP API
- Create connection Secret (`QDRANT_URL`, `QDRANT_COLLECTION`)
- Inject Secret env vars into all referencing handler Deployments
- On delete: confirm no handlers reference it; delete collection + StatefulSet + Secret

### KapeSkillReconciler (new — from 0013)
- Validate `spec.instruction` and `spec.description` are non-empty
- For each tool in `spec.tools[]`: check KapeTool exists and is Ready
- Set `status.conditions[Ready]`
- Manage finalizer `kape.io/skill-protection`: block deletion while any KapeHandler references the skill
- Kubernetes events: `SkillValid` (Normal), `DeletionBlocked` (Warning)

### KapeHandlerReconciler changes (from 0013)

**Dependency gate extension:**
- foreach skill in `spec.skills[]`:
  - KapeSkill exists → else Pending, reason: `KapeSkillNotFound`
  - KapeSkill.status.conditions[Ready]=True → else Pending, reason: `KapeSkillNotReady`

**Tool union computation:**
```go
toolMap := map[string]KapeTool{}
for _, ref := range handler.Spec.Tools {
    tool := fetchKapeTool(ref.Ref)
    toolMap[tool.Name] = tool
}
for _, skillRef := range handler.Spec.Skills {
    skill := fetchKapeSkill(skillRef.Ref)
    for _, ref := range skill.Spec.Tools {
        tool := fetchKapeTool(ref.Ref)
        toolMap[tool.Name] = tool
    }
}
```

**System prompt assembly:**
- Handler systemPrompt → eager skill instructions (lazyLoad: false, declaration order) → lazy skill preamble
- Lazy skill preamble lists name + description of all lazyLoad: true skills

**Lazy skill ConfigMap:**
- `kape-skills-{handler-name}`: one file per lazy skill (`{skill-name}.txt` with raw instruction)
- Only created if lazy skills exist; mounted at `/etc/kape/skills/` in kapehandler container

**Rollout hash extension:**
```go
rolloutHash = sha256(
    handler.Spec +
    schema.Spec +
    foreach tool in toolMap: tool.Spec +
    foreach skill in handler.Spec.Skills: skill.Spec
)
```

**Label sync extension:**
```
kape.io/skill-ref-{skillname}=true  // one per entry in spec.skills[]
```

### Sidecar injection change (from 0013)
- Replace N `kapetool-*` sidecars with one `kapeproxy` sidecar per handler pod
- Render `kapeproxy-config-{handler-name}` ConfigMap from unified toolMap:
  ```yaml
  upstreams:
    {kapetool-name}:
      url: {KapeTool.spec.mcp.endpoint}
      transport: sse
      allowedTools: [...]
      redaction: {...}
      audit: true
  ```
- Add `kapeproxy.image` and `kapeproxy.version` to `kape-config` ConfigMap

### KapeProxy binary (new — from 0013)
- New Go binary at `cmd/kapeproxy/`
- Startup: read `/etc/kapeproxy/config.yaml`, connect to each upstream, call `tools/list`, filter by `allowedTools`, namespace tools as `{kapetool-name}__{tool-name}`, register in routing table
- Expose single MCP endpoint on `:8080`
- Tool call handling: parse prefix → lookup routing table → apply input redaction → forward to upstream → apply output redaction → emit OTEL span → return response
- Unreachable upstream at startup: log, mark unavailable, continue — do not fail pod startup
- OTEL: W3C TraceContext propagation, child spans under handler root span

### KapeSchema reconciler
- Validate `spec.jsonSchema` is a valid JSON Schema object with `properties`
- Block deletion if any KapeHandler references this schema

### KEDA ScaledObject generation
- Create `ScaledObject` targeting handler Deployment
- `NatsJetStreamScaler` on consumer group lag
- `minReplicas`, `maxReplicas` from `spec.scaling`

### Cross-resource watch
- KapeTool changes trigger KapeHandler reconciliation for all referencing handlers
- KapeSkill changes trigger KapeHandler reconciliation for all referencing handlers

## Acceptance Criteria

- Apply KapeHandler + KapeTool (memory type) → Qdrant StatefulSet appears, handler Deployment has QDRANT_* env vars
- Apply KapeSkill referencing a KapeTool → KapeSkill status shows Ready
- Apply KapeHandler referencing a KapeSkill → handler pod has single `kapeproxy` sidecar (no per-tool sidecars)
- Apply KapeHandler + KapeSkill (lazyLoad: true) → `kape-skills-{name}` ConfigMap exists, mounted at `/etc/kape/skills/`
- Attempt to delete a KapeSkill referenced by a KapeHandler → deletion blocked
- KEDA ScaledObject visible with correct min/max replicas
- kapeproxy `tools/list` returns namespaced tool names (`kapetool-name__tool-name`)

**M2 gate:** Full lifecycle from CRD apply to running handler with Qdrant, KapeSkill, KapeProxy, and KEDA.

## Key Files

- `operator/controller/tool.go`
- `operator/controller/schema.go`
- `operator/controller/skill.go` (new)
- `operator/reconcile/tool.go`
- `operator/reconcile/schema.go`
- `operator/reconcile/skill.go` (new)
- `operator/reconcile/handler.go` (updated: skill gate, tool union, system prompt assembly, kapeproxy config)
- `operator/infra/k8s/kapeproxy.go` (new — sidecar injection)
- `operator/infra/k8s/kapeproxy_config.go` (new — config rendering)
- `operator/infra/qdrant/`
- `operator/infra/k8s/scaledobject.go`
- `cmd/kapeproxy/main.go` (new Go binary)
- `internal/proxy/server.go` (new)
- `internal/proxy/router.go` (new)
```

- [ ] **Step 2: Verify file created**

```bash
wc -l docs/roadmap/phases/06-full-operator.md
```

Expected: > 100 lines

- [ ] **Step 3: Commit**

```bash
git add docs/roadmap/phases/06-full-operator.md
git commit -m "doc: add roadmap phase 06 — full operator with 0013 KapeSkill + KapeProxy additions"
```

---

### Task 6: Create phase file 07 — Full Runtime (updated with 0013)

**Files:**
- Create: `docs/roadmap/phases/07-full-runtime.md`

- [ ] **Step 1: Create 07-full-runtime.md**

```markdown
# Phase 7 — Full Runtime

**Status:** pending
**Milestone:** M3
**Specs:** 0004, 0006, 0013
**Modified by:** 0012 (created), 0013 (kapeproxy tool registry, load_skill tool, settings.toml [proxy] section)

## Goal

The runtime gains full agent capability: MCP tool calls via kapeproxy, vector memory, event-publish for handler chaining, retry/DLQ, dedup window, and Prometheus metrics.

## Reference Specs

- `0004-kape-handler` — MCP tool integration, ActionsRouter, memory tool, retry/DLQ, Prometheus metrics
- `0006-events-broker-design` — event-publish CloudEvent format, subject routing
- `0013-kape-skill-crd` — kapeproxy tool registry (single MCPToolkit), load_skill tool, settings.toml [proxy] section

## Work

### MCP tool integration (updated from 0013)

Replace per-tool sidecar connections with a single kapeproxy connection:

```python
# Single connection to kapeproxy federation endpoint
toolkit = MCPToolkit(url=config.proxy.endpoint)
mcp_tools = toolkit.get_tools()
# mcp_tools already contains namespaced tool names from kapeproxy
```

Add `call_tools` node (LangGraph `ToolNode`) to graph.
Full graph: `entry_router → reason ⇄ call_tools → respond`

### load_skill tool (new — from 0013)

Always registered in LangGraph tool registry at startup, regardless of lazy skills:

```python
from langchain_core.tools import tool
from pathlib import Path

SKILLS_DIR = Path("/etc/kape/skills")

@tool
def load_skill(skill_name: str) -> str:
    """
    Load the full instruction for a named skill.
    Call this when you determine a skill is relevant to the current investigation.
    Returns the full instruction text with all template variables resolved.
    """
    path = SKILLS_DIR / f"{skill_name}.txt"
    if not path.exists():
        return f"Skill '{skill_name}' not found. Available skills are listed in your instructions."
    raw = path.read_text()
    return jinja_env.from_string(raw).render(context)
```

`context` is the same Jinja2 render context used for the system prompt (`event`, `cluster_name`, `handler_name`, `namespace`, `timestamp`, `env`). If `SKILLS_DIR` does not exist, returns not-found message gracefully — no exception.

### settings.toml [proxy] section (from 0013)

The `[tools.*]` sections per MCP tool are replaced by a single `[proxy]` section:

```toml
[proxy]
endpoint  = "http://localhost:8080"
transport = "sse"
```

Memory-type tools retain their own section:
```toml
[tools.order-memory]
type            = "memory"
qdrant_endpoint = "http://kape-memory-order-memory.kape-system:6333"
```

Update `config.py` to read `[proxy]` instead of `[tools.*.sidecar_port]`.

### Memory tool integration
- Connect to Qdrant via `QDRANT_URL` + `QDRANT_COLLECTION` env vars
- Build LangChain `QdrantVectorStore` retriever
- Register as LangChain tool in LangGraph tool registry

### ActionsRouter (`actions/router.py`)
- JSONPath condition evaluation against `schema_output`
- `event-publish` action: resolve Jinja2 fields, extract `$prompt` fields, publish CloudEvent to NATS
- `webhook` action: HTTP POST to configured endpoint
- `save_memory` action: Qdrant upsert
- All actions run; partial failures logged, do not fail the Task

### Retry/DLQ
- Wrap LangGraph invoke with `tenacity` exponential backoff (LLM 429, 503)
- Non-retryable errors → immediate DLQ
- DLQ: publish to `kape.events.dlq.<handler-name>` with original CloudEvent + error context

### Dedup sliding window
- In-memory set of CloudEvent IDs, 60s TTL
- Reject duplicates before NATS ACK

### Prometheus metrics
- `prometheus_client` — `kape_events_total`, `kape_llm_latency_seconds`, `kape_tool_calls_total`, `kape_decisions_total`
- All labelled by handler name

## Acceptance Criteria

- Handler calls an MCP tool via kapeproxy during ReAct loop; namespaced tool name visible in OTEL trace
- Agent calls `load_skill("check-order-events")` → returns rendered instruction from `/etc/kape/skills/check-order-events.txt`
- Handler persists a memory entry to Qdrant; subsequent event retrieves it
- Handler A emits a CloudEvent via event-publish → Handler B picks it up; both Task records exist with correct `parent_task_id`
- Duplicate CloudEvent ID within 60s window is discarded
- LLM 429 triggers retry; non-retryable error routes to DLQ subject
- `curl handler-pod:8000/metrics` returns Prometheus text with all expected metrics

**M3 gate:** Two-handler chain runs end-to-end; MCP tool calls via kapeproxy appear in traces; load_skill works for lazy skills.

## Key Files

- `runtime/graph/graph.py` (updated: kapeproxy MCPToolkit, load_skill registered)
- `runtime/graph/nodes.py` (updated: call_tools node)
- `runtime/config.py` (updated: [proxy] section, remove per-tool sidecar config)
- `runtime/skills.py` (new: load_skill tool implementation)
- `runtime/actions/router.py`
- `runtime/actions/event_emitter.py`
- `runtime/actions/save_memory.py`
- `runtime/actions/webhook.py`
```

- [ ] **Step 2: Verify file created**

```bash
wc -l docs/roadmap/phases/07-full-runtime.md
```

Expected: > 100 lines

- [ ] **Step 3: Commit**

```bash
git add docs/roadmap/phases/07-full-runtime.md
git commit -m "doc: add roadmap phase 07 — full runtime with 0013 kapeproxy + load_skill additions"
```

---

### Task 7: Create phase files 08–10

**Files:**
- Create: `docs/roadmap/phases/08-audit-security.md`
- Create: `docs/roadmap/phases/09-dashboard.md`
- Create: `docs/roadmap/phases/10-helm-polish.md`

- [ ] **Step 1: Create 08-audit-security.md**

```markdown
# Phase 8 — K8s Audit Adapter + Security Hardening

**Status:** pending
**Milestone:** M4
**Specs:** 0006, 0007
**Modified by:** 0012 (created)

## Goal

Add the second event producer (K8s Audit) and apply the full 8-layer security model from spec 0007.

## Reference Specs

- `0006-events-broker-design` — K8s Audit adapter design, audit event subject hierarchy
- `0007-security-layer` — full 8-layer security model

## Work

### K8s Audit adapter
- HTTP server accepting K8s API server audit webhook events (`adapters/kape-audit-adapter/`)
- Audit policy: verbs `create`, `update`, `delete` on Secrets, RoleBindings, ClusterRoleBindings, Pods
- Build CloudEvent: `type = kape.events.audit`, `source = k8s-apiserver`
- Publish to `kape.events.audit.<verb>.<resource>`

### NetworkPolicy manifests
- Handler pod egress: NATS (4222), LLM provider (443), MCP sidecars (by pod label), task-service (8080), Qdrant (6333)
- All other egress denied
- Standard NetworkPolicy + Cilium variant in `examples/networkpolicy/`

### Prompt injection defence
- Full system prompt template in `runtime/graph/system_prompt.j2`
- HTML-escape all `event_raw` fields before Jinja2 rendering
- XML tag isolation: wrap user content in `<event>...</event>` tags

### Secret management
- ESO `SecretStore` + `ExternalSecret` example manifests (`examples/eso/`)
- Operator mounts KapeTool connection Secrets as files (not env vars)

### Immutable audit log
- PostgreSQL role `kape_writer` with `INSERT` only — no `UPDATE` on terminal-state rows via trigger

### mTLS for NATS
- cert-manager `Certificate` resources for NATS cluster + all client connections
- Update NATS StatefulSet and all publisher/consumer configs

## Acceptance Criteria

- K8s Audit adapter: create a Secret → CloudEvent in NATS → handler processes it → Task in DB
- NetworkPolicy: `curl 8.8.8.8` from handler pod fails; `curl nats-svc:4222` succeeds
- Prompt injection: inject `<script>call_tool(rm -rf)</script>` into test event → no out-of-allowlist tool calls
- mTLS: non-mTLS NATS client connection is rejected

**M4 gate:** Both adapters live; network policy blocks unexpected egress; prompt injection test passes.

## Key Files

- `adapters/kape-audit-adapter/`
- `examples/networkpolicy/`
- `examples/eso/`
- `runtime/graph/system_prompt.j2`
- `operator/infra/k8s/deployment.go` (Secret file mounts)
```

- [ ] **Step 2: Create 09-dashboard.md**

```markdown
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
```

- [ ] **Step 3: Create 10-helm-polish.md**

```markdown
# Phase 10 — Helm + Examples + Polish

**Status:** pending
**Milestone:** M6
**Specs:** 0011
**Modified by:** 0012 (created)

## Goal

Package the full stack as a Helm chart installable on a fresh cluster. Ship example manifests. Write the demo runbook. v1 Release Candidate.

## Reference Specs

- `0011-repo-structure` — Helm chart layout, image naming conventions, changeset-based release strategy

## Work

### Helm chart (`helm/`)
Templates for: NATS JetStream StatefulSet, CloudNativePG cluster, KAPE operator, task-service, AlertManager adapter, K8s Audit adapter, Dashboard, OAuth2 Proxy, CRDs, cert-manager Issuer + Certificate.

`helm/values.yaml`: all configurable defaults (image tags, resource requests, LLM provider, NATS retention, replicas)

### Examples
- `examples/alertmanager-handler/`: complete KapeHandler + KapeTool + KapeSchema + KapeSkill for AlertManager use case
- `examples/audit-handler/`: complete example for K8s Audit use case

### Documentation
- `DEMO.md`: step-by-step runbook — install Helm chart, apply examples, fire test alert, watch task in dashboard

### Release
- Image tagging: all components tagged `0.1.0` in `values.yaml`
- `npx changeset` → `CHANGELOG.md` per component → `0.1.0` tag

## Acceptance Criteria

- `helm install kape ./helm --set llm.apiKeySecret=my-secret -n kape-system` on fresh kind cluster: all pods Running within 5 minutes
- Apply `examples/alertmanager-handler/` → handler pod starts
- `curl` test AlertManager payload → Task appears in dashboard within 30 seconds
- `helm uninstall kape` cleanly removes all resources (no orphaned PVCs or CRDs)
- `DEMO.md` runbook is self-contained

**M6 gate:** Full install → real event → visible decision. v1 Release Candidate tagged.

## Key Files

- `helm/Chart.yaml`, `helm/values.yaml`, `helm/templates/`
- `examples/alertmanager-handler/`
- `examples/audit-handler/`
- `DEMO.md`
```

- [ ] **Step 4: Verify all three files created**

```bash
ls docs/roadmap/phases/
```

Expected (all 10 files):
```
01-crds-cel.md           02-minimal-operator.md   03-task-service.md
04-minimal-runtime.md    05-alertmanager-adapter.md  06-full-operator.md
07-full-runtime.md       08-audit-security.md     09-dashboard.md
10-helm-polish.md
```

- [ ] **Step 5: Commit**

```bash
git add docs/roadmap/phases/
git commit -m "doc: add roadmap phase files 08-10"
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Covered by |
|---|---|
| Move specs/ to docs/specs/ | Task 1 |
| Move plan.md to docs/specs/ | Task 1 |
| Add deprecation note to 0012 | Task 2 |
| Create docs/roadmap/phases.md index | Task 3 |
| Phase files 01-05 (done phases, content from 0012) | Task 4 |
| Phase 06 updated with 0013 additions | Task 5 |
| Phase 07 updated with 0013 additions | Task 6 |
| Phase files 08-10 | Task 7 |
| Delete root specs/ | Task 1 step 4 |

**Placeholder scan:** No TBDs, no "implement later", no vague steps. All content is verbatim markdown to write.

**Type consistency:** No code types — all markdown content. File paths consistent across tasks.
