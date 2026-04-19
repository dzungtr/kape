# Roadmap Iteration Breakdown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert pending roadmap phases 7–10 from monolithic phase files into subdirectories of single-concern iteration files, each targeting fewer than 20 file changes per PR.

**Architecture:** Each pending phase (07–10) becomes a directory. The existing phase `.md` file becomes `README.md` inside the directory. Each logical concern within the phase gets its own numbered iteration file with goal, work items, acceptance criteria, and key files.

**Tech Stack:** Markdown, Git

---

## File Map

**Create directories:**
- `docs/roadmap/phases/07-full-runtime/`
- `docs/roadmap/phases/08-audit-security/`
- `docs/roadmap/phases/09-dashboard/`
- `docs/roadmap/phases/10-helm-polish/`

**Rename (move) existing phase files to README.md inside new directories:**
- `docs/roadmap/phases/07-full-runtime.md` → `docs/roadmap/phases/07-full-runtime/README.md`
- `docs/roadmap/phases/08-audit-security.md` → `docs/roadmap/phases/08-audit-security/README.md`
- `docs/roadmap/phases/09-dashboard.md` → `docs/roadmap/phases/09-dashboard/README.md`
- `docs/roadmap/phases/10-helm-polish.md` → `docs/roadmap/phases/10-helm-polish/README.md`

**Create iteration files (22 total):**

Phase 7 (7 files):
- `docs/roadmap/phases/07-full-runtime/01-proxy-config.md`
- `docs/roadmap/phases/07-full-runtime/02-kapeproxy-mcp-integration.md`
- `docs/roadmap/phases/07-full-runtime/03-load-skill-tool.md`
- `docs/roadmap/phases/07-full-runtime/04-memory-tool.md`
- `docs/roadmap/phases/07-full-runtime/05-actions-router.md`
- `docs/roadmap/phases/07-full-runtime/06-retry-dlq.md`
- `docs/roadmap/phases/07-full-runtime/07-dedup-metrics.md`

Phase 8 (6 files):
- `docs/roadmap/phases/08-audit-security/01-k8s-audit-adapter.md`
- `docs/roadmap/phases/08-audit-security/02-network-policy.md`
- `docs/roadmap/phases/08-audit-security/03-prompt-injection-defense.md`
- `docs/roadmap/phases/08-audit-security/04-secret-management.md`
- `docs/roadmap/phases/08-audit-security/05-immutable-audit-log.md`
- `docs/roadmap/phases/08-audit-security/06-mtls-nats.md`

Phase 9 (5 files):
- `docs/roadmap/phases/09-dashboard/01-type-generation.md`
- `docs/roadmap/phases/09-dashboard/02-task-list-sse.md`
- `docs/roadmap/phases/09-dashboard/03-task-detail.md`
- `docs/roadmap/phases/09-dashboard/04-handler-routes.md`
- `docs/roadmap/phases/09-dashboard/05-auth-deploy.md`

Phase 10 (4 files):
- `docs/roadmap/phases/10-helm-polish/01-helm-infra-templates.md`
- `docs/roadmap/phases/10-helm-polish/02-helm-app-templates.md`
- `docs/roadmap/phases/10-helm-polish/03-examples.md`
- `docs/roadmap/phases/10-helm-polish/04-docs-release.md`

---

## Task 1: Phase 7 — Full Runtime

**Files:**
- Rename: `docs/roadmap/phases/07-full-runtime.md` → `docs/roadmap/phases/07-full-runtime/README.md`
- Create: `docs/roadmap/phases/07-full-runtime/01-proxy-config.md`
- Create: `docs/roadmap/phases/07-full-runtime/02-kapeproxy-mcp-integration.md`
- Create: `docs/roadmap/phases/07-full-runtime/03-load-skill-tool.md`
- Create: `docs/roadmap/phases/07-full-runtime/04-memory-tool.md`
- Create: `docs/roadmap/phases/07-full-runtime/05-actions-router.md`
- Create: `docs/roadmap/phases/07-full-runtime/06-retry-dlq.md`
- Create: `docs/roadmap/phases/07-full-runtime/07-dedup-metrics.md`

- [ ] **Step 1: Move existing phase file to README.md**

```bash
mkdir -p docs/roadmap/phases/07-full-runtime
git mv docs/roadmap/phases/07-full-runtime.md docs/roadmap/phases/07-full-runtime/README.md
```

- [ ] **Step 2: Create `01-proxy-config.md`**

Create `docs/roadmap/phases/07-full-runtime/01-proxy-config.md` with content:

```markdown
# Phase 7.1 — Proxy Config

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0004, 0013

## Goal

Update `config.py` to read a single `[proxy]` section from `settings.toml`, replacing the per-tool `[tools.*.sidecar_port]` configuration. Memory-type tools retain their own `[tools.*]` section.

## Work

- Update `Config` dataclass: replace `tools: dict[str, ToolConfig]` sidecar fields with `proxy: ProxyConfig` (fields: `endpoint: str`, `transport: str`)
- Add `ProxyConfig` dataclass
- Memory-type tool config remains under `[tools.<name>]` with `type = "memory"` — only sidecar/MCP tool config is removed
- Update example `settings.toml` to reflect new schema:
  ```toml
  [proxy]
  endpoint  = "http://localhost:8080"
  transport = "sse"

  [tools.order-memory]
  type            = "memory"
  qdrant_endpoint = "http://kape-memory-order-memory.kape-system:6333"
  ```
- Update unit tests for config loading

## Acceptance Criteria

- `Config` loads `proxy.endpoint` and `proxy.transport` from `[proxy]` section
- Memory-type tool config still loads correctly from `[tools.*]`
- Old `sidecar_port` config key is removed; loading a settings.toml with it raises a clear validation error

## Key Files

- `runtime/config.py` (modified)
- `runtime/tests/test_config.py` (modified)
```

- [ ] **Step 3: Create `02-kapeproxy-mcp-integration.md`**

Create `docs/roadmap/phases/07-full-runtime/02-kapeproxy-mcp-integration.md` with content:

```markdown
# Phase 7.2 — Kapeproxy MCP Integration

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0004, 0013

## Goal

Replace per-tool sidecar MCPToolkit connections with a single `MCPToolkit` connecting to the kapeproxy federation endpoint. Wire a `call_tools` LangGraph `ToolNode` into the graph, completing the full `entry_router → reason ⇄ call_tools → respond` loop.

## Work

- Replace multiple per-tool `MCPToolkit` instantiations with one:
  ```python
  toolkit = MCPToolkit(url=config.proxy.endpoint)
  mcp_tools = toolkit.get_tools()
  ```
- Add `call_tools` node using LangGraph `ToolNode(mcp_tools)`
- Wire full graph: `entry_router → reason → call_tools → reason` (loop) `→ respond`
- Add conditional edge from `reason`: if `tool_calls` present → `call_tools`, else → `respond`
- Remove all per-tool sidecar connection code

## Acceptance Criteria

- Handler calls an MCP tool via kapeproxy during ReAct loop
- Namespaced tool name (e.g. `kapetool-name__tool-name`) visible in OTEL trace
- Graph terminates at `respond` when LLM produces no tool calls

## Key Files

- `runtime/graph/graph.py` (modified)
- `runtime/graph/nodes.py` (modified — add call_tools node)
- `runtime/tests/test_graph.py` (modified)
```

- [ ] **Step 4: Create `03-load-skill-tool.md`**

Create `docs/roadmap/phases/07-full-runtime/03-load-skill-tool.md` with content:

```markdown
# Phase 7.3 — Load Skill Tool

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0013

## Goal

Implement the `load_skill` LangChain tool that reads lazy skill instructions from `/etc/kape/skills/` and renders them via Jinja2. Register it in the graph tool registry at startup regardless of whether lazy skills exist.

## Work

- Create `runtime/skills.py`:
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
- `jinja_env` and `context` are the same Jinja2 env and render context used for system prompt rendering (passed in at module init or via closure)
- If `SKILLS_DIR` does not exist: `load_skill` returns not-found message, no exception
- Register `load_skill` in graph tool registry alongside `mcp_tools` in `graph.py`

## Acceptance Criteria

- Agent calls `load_skill("check-order-events")` → returns rendered instruction from `/etc/kape/skills/check-order-events.txt`
- `load_skill("nonexistent")` → returns not-found message string, no exception
- `SKILLS_DIR` missing entirely → same not-found behaviour, no exception

## Key Files

- `runtime/skills.py` (new)
- `runtime/graph/graph.py` (modified — register load_skill)
- `runtime/tests/test_skills.py` (new)
```

- [ ] **Step 5: Create `04-memory-tool.md`**

Create `docs/roadmap/phases/07-full-runtime/04-memory-tool.md` with content:

```markdown
# Phase 7.4 — Memory Tool

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0004

## Goal

Connect to Qdrant via environment variables and register a `QdrantVectorStore` retriever as a LangChain tool in the graph tool registry.

## Work

- Create `runtime/memory.py`:
  - Read `QDRANT_URL` and `QDRANT_COLLECTION` from env
  - Build `QdrantVectorStore` client
  - Wrap as a LangChain `Tool` with name `search_memory` and description for the LLM
  - Return `None` if env vars not set (handler may not have a memory tool configured)
- In `graph.py`: if `memory_tool := build_memory_tool()` is not None, append to tool registry
- Write integration test using a local Qdrant instance (or mock)

## Acceptance Criteria

- Handler persists a memory entry to Qdrant via `save_memory` action (Phase 7.5)
- Subsequent event retrieves the stored entry via `search_memory` tool call
- No memory tool configured (`QDRANT_URL` unset) → graph starts normally, `search_memory` absent from tool list

## Key Files

- `runtime/memory.py` (new)
- `runtime/graph/graph.py` (modified — register memory tool)
- `runtime/tests/test_memory.py` (new)
```

- [ ] **Step 6: Create `05-actions-router.md`**

Create `docs/roadmap/phases/07-full-runtime/05-actions-router.md` with content:

```markdown
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
```

- [ ] **Step 7: Create `06-retry-dlq.md`**

Create `docs/roadmap/phases/07-full-runtime/06-retry-dlq.md` with content:

```markdown
# Phase 7.6 — Retry + DLQ

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0004

## Goal

Wrap LangGraph invocation with `tenacity` exponential backoff for LLM transient errors (429, 503). Route non-retryable errors immediately to a DLQ NATS subject.

## Work

- Wrap `graph.invoke()` call with `tenacity.retry`:
  - Retry on: `httpx.HTTPStatusError` with status 429 or 503; `openai.RateLimitError`
  - Wait: exponential backoff, start 1s, multiplier 2, max 30s
  - Stop: after 5 attempts
- Non-retryable errors (all others): catch, publish DLQ message, mark Task `failed`
- DLQ publish: NATS subject `kape.events.dlq.<handler-name>`, message body:
  ```json
  {
    "original_event": "<base64 CloudEvent>",
    "error": "<exception type: message>",
    "task_id": "<uuid>",
    "handler": "<handler-name>",
    "timestamp": "<ISO-8601>"
  }
  ```
- Create `runtime/dlq.py` for DLQ publish logic

## Acceptance Criteria

- Mocked LLM 429 response → retry fires; succeeds on retry → Task `completed`
- Non-retryable error → Task `failed`; message appears on `kape.events.dlq.<name>` with correct fields
- After 5 failed retries → routed to DLQ

## Key Files

- `runtime/graph/graph.py` (modified — wrap invoke with retry)
- `runtime/dlq.py` (new)
- `runtime/tests/test_retry.py` (new)
```

- [ ] **Step 8: Create `07-dedup-metrics.md`**

Create `docs/roadmap/phases/07-full-runtime/07-dedup-metrics.md` with content:

```markdown
# Phase 7.7 — Dedup + Prometheus Metrics

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0004

## Goal

Add a sliding-window dedup cache to discard duplicate CloudEvent IDs within a 60-second window. Add Prometheus metrics for events, LLM latency, tool calls, and decisions.

## Work

### Dedup
- Create `runtime/dedup.py`:
  - In-memory dict of `event_id → expiry_timestamp`
  - `is_duplicate(event_id: str) -> bool`: returns True if id seen within 60s; registers id with 60s TTL
  - Sweep expired entries on each check (no background thread needed)
- In `consumer.py`: check `is_duplicate(event.id)` before processing; if True, ACK and skip

### Prometheus Metrics
- Create `runtime/metrics.py` with `prometheus_client` counters/histograms:
  - `kape_events_total` — Counter, labels: `handler`, `status` (`processed`, `deduplicated`, `failed`)
  - `kape_llm_latency_seconds` — Histogram, labels: `handler`
  - `kape_tool_calls_total` — Counter, labels: `handler`, `tool`
  - `kape_decisions_total` — Counter, labels: `handler`, `decision`
- Expose via existing FastAPI `/metrics` endpoint in `probe.py` using `prometheus_client.generate_latest()`

## Acceptance Criteria

- Same CloudEvent ID published twice within 60s → second is ACKed and discarded, no duplicate Task
- Same CloudEvent ID published after 60s → processed normally
- `curl handler-pod:8000/metrics` returns Prometheus text with all four metric names
- All metrics labelled by handler name

## Key Files

- `runtime/dedup.py` (new)
- `runtime/metrics.py` (new)
- `runtime/consumer.py` (modified — dedup check + metrics)
- `runtime/probe.py` (modified — /metrics endpoint)
- `runtime/tests/test_dedup.py` (new)
```

- [ ] **Step 9: Verify all Phase 7 files exist**

```bash
ls docs/roadmap/phases/07-full-runtime/
```

Expected output:
```
01-proxy-config.md
02-kapeproxy-mcp-integration.md
03-load-skill-tool.md
04-memory-tool.md
05-actions-router.md
06-retry-dlq.md
07-dedup-metrics.md
README.md
```

- [ ] **Step 10: Commit Phase 7**

```bash
git add docs/roadmap/phases/07-full-runtime/
git commit -m "doc: decompose phase 7 into 7 single-concern iterations"
```

---

## Task 2: Phase 8 — Audit + Security

**Files:**
- Rename: `docs/roadmap/phases/08-audit-security.md` → `docs/roadmap/phases/08-audit-security/README.md`
- Create: `docs/roadmap/phases/08-audit-security/01-k8s-audit-adapter.md`
- Create: `docs/roadmap/phases/08-audit-security/02-network-policy.md`
- Create: `docs/roadmap/phases/08-audit-security/03-prompt-injection-defense.md`
- Create: `docs/roadmap/phases/08-audit-security/04-secret-management.md`
- Create: `docs/roadmap/phases/08-audit-security/05-immutable-audit-log.md`
- Create: `docs/roadmap/phases/08-audit-security/06-mtls-nats.md`

- [ ] **Step 1: Move existing phase file to README.md**

```bash
mkdir -p docs/roadmap/phases/08-audit-security
git mv docs/roadmap/phases/08-audit-security.md docs/roadmap/phases/08-audit-security/README.md
```

- [ ] **Step 2: Create `01-k8s-audit-adapter.md`**

Create `docs/roadmap/phases/08-audit-security/01-k8s-audit-adapter.md` with content:

```markdown
# Phase 8.1 — K8s Audit Adapter

**Status:** pending
**Phase:** 08-audit-security
**Milestone:** M4
**Specs:** 0006

## Goal

New Go adapter that accepts K8s API server audit webhook events and publishes them as CloudEvents to NATS JetStream on subject `kape.events.audit.<verb>.<resource>`.

## Work

- New service at `adapters/kape-audit-adapter/`
- Chi HTTP server; POST `/webhook` accepts K8s `EventList` audit payload
- Parse each audit event: extract `verb`, `objectRef.resource`, `objectRef.name`, `objectRef.namespace`, `requestObject`
- Audit policy scope: verbs `create`, `update`, `delete` on `secrets`, `rolebindings`, `clusterrolebindings`, `pods`
- Build CloudEvent: `type = kape.events.audit`, `source = k8s-apiserver`, subject = `kape.events.audit.<verb>.<resource>`
- Publish via shared `adapters/internal/nats/publisher.go`
- Prometheus metrics: `kape_audit_events_received_total`, `kape_audit_events_published_total`, `kape_audit_publish_errors_total`

## Acceptance Criteria

- POST a synthetic K8s audit event for a Secret creation → CloudEvent appears in NATS on `kape.events.audit.create.secrets`
- Handler pod picks up event → Task record written with `status: completed`
- Events for non-watched verbs/resources are silently dropped (no NATS publish)

## Key Files

- `adapters/kape-audit-adapter/main.go` (new)
- `adapters/kape-audit-adapter/handler.go` (new)
- `adapters/internal/cloudevents/builder.go` (modified — audit CloudEvent type)
- `adapters/internal/nats/publisher.go` (shared, unchanged)
```

- [ ] **Step 3: Create `02-network-policy.md`**

Create `docs/roadmap/phases/08-audit-security/02-network-policy.md` with content:

```markdown
# Phase 8.2 — Network Policy

**Status:** pending
**Phase:** 08-audit-security
**Milestone:** M4
**Specs:** 0007

## Goal

Restrict handler pod egress to only required destinations. All other egress denied. Ship standard K8s NetworkPolicy and a Cilium NetworkPolicy variant.

## Work

- `examples/networkpolicy/handler-egress.yaml`: standard K8s `NetworkPolicy`
  - Allow egress to: NATS port 4222, LLM provider port 443, MCP sidecars (by pod label `kape.io/role: mcp`), task-service port 8080, Qdrant port 6333
  - Default deny all other egress
- `examples/networkpolicy/handler-egress-cilium.yaml`: `CiliumNetworkPolicy` equivalent with FQDN-based LLM egress rule (e.g. `api.openai.com`)
- Add comments in each file explaining each rule's purpose

## Acceptance Criteria

- Apply policy to handler pod namespace → `curl 8.8.8.8` from handler pod fails
- `curl nats-svc:4222` from handler pod succeeds
- `curl task-service:8080` from handler pod succeeds

## Key Files

- `examples/networkpolicy/handler-egress.yaml` (new)
- `examples/networkpolicy/handler-egress-cilium.yaml` (new)
```

- [ ] **Step 4: Create `03-prompt-injection-defense.md`**

Create `docs/roadmap/phases/08-audit-security/03-prompt-injection-defense.md` with content:

```markdown
# Phase 8.3 — Prompt Injection Defense

**Status:** pending
**Phase:** 08-audit-security
**Milestone:** M4
**Specs:** 0007

## Goal

Isolate user-controlled event content from system instructions by extracting the system prompt to a Jinja2 template, HTML-escaping all `event_raw` fields before rendering, and wrapping event content in XML tags.

## Work

- Extract full system prompt string into `runtime/graph/system_prompt.j2`
- In template: wrap all event content variables with `<event>...</event>` XML tags:
  ```
  <event>
  {{ event_raw | e }}
  </event>
  ```
- `| e` filter applies HTML escaping (Jinja2 built-in `escape`)
- Apply HTML escaping to all fields sourced from the incoming CloudEvent before passing to Jinja2 render context
- Update `nodes.py` system prompt rendering to load from `system_prompt.j2`

## Acceptance Criteria

- Inject `<script>call_tool(rm -rf /)</script>` as `event_raw` content → rendered system prompt shows escaped `&lt;script&gt;...` string, no tool call triggered for that content
- System prompt renders correctly for a normal event payload

## Key Files

- `runtime/graph/system_prompt.j2` (new)
- `runtime/graph/nodes.py` (modified — load template from file, escape event fields)
- `runtime/tests/test_prompt_injection.py` (new)
```

- [ ] **Step 5: Create `04-secret-management.md`**

Create `docs/roadmap/phases/08-audit-security/04-secret-management.md` with content:

```markdown
# Phase 8.4 — Secret Management

**Status:** pending
**Phase:** 08-audit-security
**Milestone:** M4
**Specs:** 0007

## Goal

Provide External Secrets Operator (ESO) example manifests for managing KapeTool connection secrets. Update the operator to mount KapeTool connection Secrets as files instead of environment variables.

## Work

- `examples/eso/secretstore.yaml`: ESO `SecretStore` pointing to a Vault/AWS SM backend (use Vault as example)
- `examples/eso/externalsecret.yaml`: `ExternalSecret` that pulls the Qdrant connection string and creates a K8s Secret named `kape-tool-<name>-conn`
- Update `operator/infra/k8s/deployment.go`: mount KapeTool connection Secrets as files under `/etc/kape/secrets/<tool-name>/` instead of injecting as env vars
  - `QDRANT_URL` → file at `/etc/kape/secrets/<tool-name>/qdrant_url`
  - `QDRANT_COLLECTION` → file at `/etc/kape/secrets/<tool-name>/qdrant_collection`
- Update `runtime/memory.py` (from 7.4): read from files if env vars absent

## Acceptance Criteria

- `kubectl apply -f examples/eso/` succeeds on a cluster with ESO installed
- Handler Deployment shows volume mount at `/etc/kape/secrets/` not env var injection for tool secrets
- Runtime reads Qdrant connection from file path correctly

## Key Files

- `examples/eso/secretstore.yaml` (new)
- `examples/eso/externalsecret.yaml` (new)
- `operator/infra/k8s/deployment.go` (modified — file mounts)
```

- [ ] **Step 6: Create `05-immutable-audit-log.md`**

Create `docs/roadmap/phases/08-audit-security/05-immutable-audit-log.md` with content:

```markdown
# Phase 8.5 — Immutable Audit Log

**Status:** pending
**Phase:** 08-audit-security
**Milestone:** M4
**Specs:** 0007, 0008

## Goal

Prevent mutation of terminal-state Task records in PostgreSQL by creating an INSERT-only `kape_writer` role and a trigger that blocks UPDATE on rows in terminal states.

## Work

- New migration `task-service/migrations/004_immutable_audit.sql`:
  ```sql
  -- Role with INSERT-only on tasks
  CREATE ROLE kape_writer;
  GRANT INSERT ON tasks TO kape_writer;
  -- Trigger: block UPDATE on terminal-state rows
  CREATE OR REPLACE FUNCTION prevent_terminal_update()
  RETURNS TRIGGER AS $$
  BEGIN
    IF OLD.status IN ('completed', 'failed', 'low_confidence') THEN
      RAISE EXCEPTION 'Cannot update terminal-state task %', OLD.id;
    END IF;
    RETURN NEW;
  END;
  $$ LANGUAGE plpgsql;

  CREATE TRIGGER immutable_terminal_tasks
    BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION prevent_terminal_update();
  ```
- Update task-service connection config to use `kape_writer` role for write operations
- Existing `PATCH /tasks/{id}/status` transitions must all complete before terminal state; no update needed after `completed`/`failed`/`low_confidence`

## Acceptance Criteria

- `PATCH /tasks/{id}/status` to `completed` succeeds
- Subsequent `PATCH /tasks/{id}/status` to any value → HTTP 409 / DB exception
- `INSERT` as `kape_writer` succeeds; `UPDATE` as `kape_writer` fails with permission error

## Key Files

- `task-service/migrations/004_immutable_audit.sql` (new)
```

- [ ] **Step 7: Create `06-mtls-nats.md`**

Create `docs/roadmap/phases/08-audit-security/06-mtls-nats.md` with content:

```markdown
# Phase 8.6 — mTLS for NATS

**Status:** pending
**Phase:** 08-audit-security
**Milestone:** M4
**Specs:** 0007

## Goal

Enforce mutual TLS on all NATS connections. All publishers (adapters) and consumers (runtime) must present a valid client certificate. Non-mTLS connections are rejected.

## Work

- `examples/certs/nats-server-cert.yaml`: cert-manager `Certificate` for NATS server
- `examples/certs/nats-client-cert.yaml`: cert-manager `Certificate` for clients (one cert shared across adapters + runtime, or one per component)
- `examples/certs/issuer.yaml`: cert-manager `ClusterIssuer` (self-signed CA for local/kind; swap for production CA)
- Update NATS StatefulSet manifest (in `helm/templates/nats.yaml` or `examples/nats/`): add TLS + `verify: true` + `verify_and_map: true` to NATS config
- Update `adapters/internal/nats/publisher.go`: load client cert from file path (env var `NATS_TLS_CERT`, `NATS_TLS_KEY`, `NATS_TLS_CA`); pass to `nats.Connect()`
- Update `runtime/consumer.py`: load client cert from same env vars; pass to `nats.connect()`

## Acceptance Criteria

- Non-mTLS `nats sub` client connection rejected with TLS error
- mTLS client with valid cert connects and subscribes successfully
- Adapter publishes event → runtime consumer receives it over mTLS

## Key Files

- `examples/certs/nats-server-cert.yaml` (new)
- `examples/certs/nats-client-cert.yaml` (new)
- `examples/certs/issuer.yaml` (new)
- `adapters/internal/nats/publisher.go` (modified — TLS config)
- `runtime/consumer.py` (modified — TLS config)
```

- [ ] **Step 8: Verify all Phase 8 files exist**

```bash
ls docs/roadmap/phases/08-audit-security/
```

Expected output:
```
01-k8s-audit-adapter.md
02-network-policy.md
03-prompt-injection-defense.md
04-secret-management.md
05-immutable-audit-log.md
06-mtls-nats.md
README.md
```

- [ ] **Step 9: Commit Phase 8**

```bash
git add docs/roadmap/phases/08-audit-security/
git commit -m "doc: decompose phase 8 into 6 single-concern iterations"
```

---

## Task 3: Phase 9 — Dashboard

**Files:**
- Rename: `docs/roadmap/phases/09-dashboard.md` → `docs/roadmap/phases/09-dashboard/README.md`
- Create: `docs/roadmap/phases/09-dashboard/01-type-generation.md`
- Create: `docs/roadmap/phases/09-dashboard/02-task-list-sse.md`
- Create: `docs/roadmap/phases/09-dashboard/03-task-detail.md`
- Create: `docs/roadmap/phases/09-dashboard/04-handler-routes.md`
- Create: `docs/roadmap/phases/09-dashboard/05-auth-deploy.md`

- [ ] **Step 1: Move existing phase file to README.md**

```bash
mkdir -p docs/roadmap/phases/09-dashboard
git mv docs/roadmap/phases/09-dashboard.md docs/roadmap/phases/09-dashboard/README.md
```

- [ ] **Step 2: Create `01-type-generation.md`**

Create `docs/roadmap/phases/09-dashboard/01-type-generation.md` with content:

```markdown
# Phase 9.1 — TypeScript Type Generation

**Status:** pending
**Phase:** 09-dashboard
**Milestone:** M5
**Specs:** 0009, 0008

## Goal

Set up `openapi-typescript` codegen to auto-generate TypeScript types from `task-service/openapi/openapi.yaml` into `dashboard/app/types/api.ts`. Add a `gen:types` npm script so types stay in sync with the API.

## Work

- Add `openapi-typescript` to `dashboard/package.json` devDependencies
- Add script: `"gen:types": "openapi-typescript ../../task-service/openapi/openapi.yaml -o app/types/api.ts"`
- Run codegen; commit generated `dashboard/app/types/api.ts`
- Add `dashboard/app/types/api.ts` to `.gitignore` or commit it (commit preferred — reviewers can see API shape)

## Acceptance Criteria

- `cd dashboard && npm run gen:types` produces `dashboard/app/types/api.ts` with no errors
- Generated file contains TypeScript types for `Task`, `Handler`, and all API response shapes from `openapi.yaml`

## Key Files

- `dashboard/package.json` (modified)
- `dashboard/app/types/api.ts` (generated/committed)
```

- [ ] **Step 3: Create `02-task-list-sse.md`**

Create `docs/roadmap/phases/09-dashboard/02-task-list-sse.md` with content:

```markdown
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
```

- [ ] **Step 4: Create `03-task-detail.md`**

Create `docs/roadmap/phases/09-dashboard/03-task-detail.md` with content:

```markdown
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
```

- [ ] **Step 5: Create `04-handler-routes.md`**

Create `docs/roadmap/phases/09-dashboard/04-handler-routes.md` with content:

```markdown
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
```

- [ ] **Step 6: Create `05-auth-deploy.md`**

Create `docs/roadmap/phases/09-dashboard/05-auth-deploy.md` with content:

```markdown
# Phase 9.5 — Auth + Deploy

**Status:** pending
**Phase:** 09-dashboard
**Milestone:** M5
**Specs:** 0009

## Goal

Enforce GitHub OAuth2 org/team membership via OAuth2 Proxy. Ship production Dockerfile and Kubernetes deployment manifests.

## Work

- `dashboard/Dockerfile`:
  ```dockerfile
  FROM node:22-alpine AS builder
  WORKDIR /app
  COPY package*.json ./
  RUN npm ci
  COPY . .
  RUN npm run build

  FROM node:22-alpine
  WORKDIR /app
  COPY --from=builder /app/build ./build
  COPY --from=builder /app/node_modules ./node_modules
  CMD ["node", "build/server/index.js"]
  ```
- `examples/dashboard/deployment.yaml`: `Deployment` + `ClusterIP` Service in `kape-system`
- `examples/dashboard/oauth2-proxy.yaml`: OAuth2 Proxy `Deployment` + `Service`
  - `--provider=github`
  - `--github-org=<your-org>`
  - `--github-team=<your-team>` (optional)
  - `--upstream=http://dashboard:3000`
- `examples/dashboard/ingress.yaml`: `Ingress` with TLS termination pointing to OAuth2 Proxy service

## Acceptance Criteria

- `docker build -f dashboard/Dockerfile .` succeeds
- Unauthenticated request to dashboard Ingress URL redirected to GitHub OAuth flow
- GitHub account not in org/team → 403
- GitHub account in org/team → dashboard loads

## Key Files

- `dashboard/Dockerfile` (new)
- `examples/dashboard/deployment.yaml` (new)
- `examples/dashboard/oauth2-proxy.yaml` (new)
- `examples/dashboard/ingress.yaml` (new)
```

- [ ] **Step 7: Verify all Phase 9 files exist**

```bash
ls docs/roadmap/phases/09-dashboard/
```

Expected output:
```
01-type-generation.md
02-task-list-sse.md
03-task-detail.md
04-handler-routes.md
05-auth-deploy.md
README.md
```

- [ ] **Step 8: Commit Phase 9**

```bash
git add docs/roadmap/phases/09-dashboard/
git commit -m "doc: decompose phase 9 into 5 single-concern iterations"
```

---

## Task 4: Phase 10 — Helm + Polish

**Files:**
- Rename: `docs/roadmap/phases/10-helm-polish.md` → `docs/roadmap/phases/10-helm-polish/README.md`
- Create: `docs/roadmap/phases/10-helm-polish/01-helm-infra-templates.md`
- Create: `docs/roadmap/phases/10-helm-polish/02-helm-app-templates.md`
- Create: `docs/roadmap/phases/10-helm-polish/03-examples.md`
- Create: `docs/roadmap/phases/10-helm-polish/04-docs-release.md`

- [ ] **Step 1: Move existing phase file to README.md**

```bash
mkdir -p docs/roadmap/phases/10-helm-polish
git mv docs/roadmap/phases/10-helm-polish.md docs/roadmap/phases/10-helm-polish/README.md
```

- [ ] **Step 2: Create `01-helm-infra-templates.md`**

Create `docs/roadmap/phases/10-helm-polish/01-helm-infra-templates.md` with content:

```markdown
# Phase 10.1 — Helm Infrastructure Templates

**Status:** pending
**Phase:** 10-helm-polish
**Milestone:** M6
**Specs:** 0011

## Goal

Helm templates for stateful infrastructure: NATS JetStream StatefulSet, CloudNativePG cluster, cert-manager `ClusterIssuer` + `Certificate` resources, and CRD install job.

## Work

- `helm/Chart.yaml`: chart name `kape`, version `0.1.0`, appVersion `0.1.0`
- `helm/values.yaml` (initial): infra-related defaults
  ```yaml
  nats:
    replicas: 3
    storage: 10Gi
  postgres:
    instances: 1
    storage: 20Gi
  certManager:
    enabled: true
    issuerEmail: "admin@example.com"
  ```
- `helm/templates/nats.yaml`: NATS JetStream `StatefulSet` + headless `Service` + `ConfigMap` for nats-server config
- `helm/templates/cnpg.yaml`: CloudNativePG `Cluster` resource
- `helm/templates/certmanager.yaml`: `ClusterIssuer` (Let's Encrypt staging + production) + `Certificate` for NATS server
- `helm/templates/crds.yaml`: `Job` to `kubectl apply -f crds/` on install (or use Helm CRD install hook)

## Acceptance Criteria

- `helm template kape ./helm` renders all infra resources without errors
- `helm install kape ./helm -n kape-system` on fresh kind cluster: NATS pods Running, CNPG cluster Ready
- `helm uninstall kape` removes all resources (no orphaned PVCs without explicit `persistence.retain`)

## Key Files

- `helm/Chart.yaml` (new)
- `helm/values.yaml` (new)
- `helm/templates/nats.yaml` (new)
- `helm/templates/cnpg.yaml` (new)
- `helm/templates/certmanager.yaml` (new)
- `helm/templates/crds.yaml` (new)
```

- [ ] **Step 3: Create `02-helm-app-templates.md`**

Create `docs/roadmap/phases/10-helm-polish/02-helm-app-templates.md` with content:

```markdown
# Phase 10.2 — Helm Application Templates

**Status:** pending
**Phase:** 10-helm-polish
**Milestone:** M6
**Specs:** 0011

## Goal

Helm templates for all application components: operator, task-service, AlertManager adapter, K8s Audit adapter, dashboard, and OAuth2 Proxy. Complete `values.yaml` with all configurable defaults including image tags.

## Work

- `helm/templates/operator.yaml`: operator `Deployment` + `ServiceAccount` + `ClusterRole` + `ClusterRoleBinding`
- `helm/templates/task-service.yaml`: task-service `Deployment` + `Service` + `ConfigMap` for DB connection
- `helm/templates/alertmanager-adapter.yaml`: adapter `Deployment` + `Service`
- `helm/templates/audit-adapter.yaml`: adapter `Deployment` + `Service`
- `helm/templates/dashboard.yaml`: dashboard `Deployment` + `Service`
- `helm/templates/oauth2-proxy.yaml`: OAuth2 Proxy `Deployment` + `Service` + `Ingress`
- `helm/values.yaml` (completed):
  ```yaml
  images:
    operator: ghcr.io/kape-io/operator:0.1.0
    taskService: ghcr.io/kape-io/task-service:0.1.0
    alertmanagerAdapter: ghcr.io/kape-io/alertmanager-adapter:0.1.0
    auditAdapter: ghcr.io/kape-io/audit-adapter:0.1.0
    dashboard: ghcr.io/kape-io/dashboard:0.1.0
  llm:
    provider: openai
    apiKeySecret: ""  # required
  ```

## Acceptance Criteria

- `helm install kape ./helm --set llm.apiKeySecret=my-secret -n kape-system` on fresh kind cluster: all pods Running within 5 minutes
- `helm uninstall kape` cleanly removes all application resources

## Key Files

- `helm/templates/operator.yaml` (new)
- `helm/templates/task-service.yaml` (new)
- `helm/templates/alertmanager-adapter.yaml` (new)
- `helm/templates/audit-adapter.yaml` (new)
- `helm/templates/dashboard.yaml` (new)
- `helm/templates/oauth2-proxy.yaml` (new)
- `helm/values.yaml` (updated — image tags + llm config)
```

- [ ] **Step 4: Create `03-examples.md`**

Create `docs/roadmap/phases/10-helm-polish/03-examples.md` with content:

```markdown
# Phase 10.3 — Examples

**Status:** pending
**Phase:** 10-helm-polish
**Milestone:** M6
**Specs:** 0011

## Goal

Ship complete, working example manifests for the two primary use cases: AlertManager handler and K8s Audit handler. Each example is self-contained and can be applied directly after Helm install.

## Work

### `examples/alertmanager-handler/`
- `kapehandler.yaml`: `KapeHandler` with LLM config, `spec.tools` referencing the memory tool, `spec.skills` referencing the triage skill
- `kapetool.yaml`: `KapeTool` of type `memory` named `alert-memory`
- `kapeschema.yaml`: `KapeSchema` with JSON schema for alert triage output (`severity`, `action`, `summary` fields)
- `kapeskill.yaml`: `KapeSkill` named `alert-triage` with `spec.instruction` for alert analysis

### `examples/audit-handler/`
- `kapehandler.yaml`: `KapeHandler` configured for K8s audit events
- `kapetool.yaml`: `KapeTool` of type `mcp` referencing a kubectl MCP tool endpoint
- `kapeschema.yaml`: `KapeSchema` for audit response (`risk_level`, `action`, `justification`)
- `kapeskill.yaml`: `KapeSkill` named `audit-review` for security audit analysis

## Acceptance Criteria

- `kubectl apply -f examples/alertmanager-handler/` → all resources created; KapeHandler reaches `Ready` status
- `kubectl apply -f examples/audit-handler/` → all resources created; KapeHandler reaches `Ready` status
- Firing a test alert → Task appears in dashboard with populated `schema_output`

## Key Files

- `examples/alertmanager-handler/kapehandler.yaml` (new)
- `examples/alertmanager-handler/kapetool.yaml` (new)
- `examples/alertmanager-handler/kapeschema.yaml` (new)
- `examples/alertmanager-handler/kapeskill.yaml` (new)
- `examples/audit-handler/kapehandler.yaml` (new)
- `examples/audit-handler/kapetool.yaml` (new)
- `examples/audit-handler/kapeschema.yaml` (new)
- `examples/audit-handler/kapeskill.yaml` (new)
```

- [ ] **Step 5: Create `04-docs-release.md`**

Create `docs/roadmap/phases/10-helm-polish/04-docs-release.md` with content:

```markdown
# Phase 10.4 — Docs + Release

**Status:** pending
**Phase:** 10-helm-polish
**Milestone:** M6
**Specs:** 0011

## Goal

Write the self-contained demo runbook. Generate `CHANGELOG.md` per component via changesets. Tag `0.1.0` as the v1 Release Candidate.

## Work

- `DEMO.md` at repo root:
  1. Prerequisites (kind, helm, kubectl, nats-cli)
  2. `helm install kape ./helm --set llm.apiKeySecret=<your-key> -n kape-system --create-namespace`
  3. `kubectl apply -f examples/alertmanager-handler/`
  4. Fire test AlertManager payload via `curl`
  5. Watch Task appear in dashboard
  6. `helm uninstall kape` cleanup
- Run `npx changeset` for each component; commit `CHANGELOG.md` files:
  - `adapters/CHANGELOG.md`
  - `operator/CHANGELOG.md`
  - `task-service/CHANGELOG.md`
  - `runtime/CHANGELOG.md`
- Tag repo: `git tag v0.1.0`

## Acceptance Criteria

- `DEMO.md` runbook is self-contained: a new engineer can follow it on a fresh machine with only the prerequisites installed
- `helm uninstall kape` leaves no orphaned PVCs, CRDs, or namespaces
- `v0.1.0` tag exists on main

## Key Files

- `DEMO.md` (new)
- `adapters/CHANGELOG.md` (new)
- `operator/CHANGELOG.md` (new)
- `task-service/CHANGELOG.md` (new)
- `runtime/CHANGELOG.md` (new)
```

- [ ] **Step 6: Verify all Phase 10 files exist**

```bash
ls docs/roadmap/phases/10-helm-polish/
```

Expected output:
```
01-helm-infra-templates.md
02-helm-app-templates.md
03-examples.md
04-docs-release.md
README.md
```

- [ ] **Step 7: Commit Phase 10**

```bash
git add docs/roadmap/phases/10-helm-polish/
git commit -m "doc: decompose phase 10 into 4 single-concern iterations"
```

---

## Final Verification

- [ ] **Verify overall structure**

```bash
find docs/roadmap/phases -type f | sort
```

Expected: 5 flat `.md` files (phases 01–05) + 4 directories each with `README.md` + iteration files (26 files total in subdirs).

- [ ] **Verify no orphaned phase files**

```bash
ls docs/roadmap/phases/*.md
```

Expected: only `01-crds-cel.md`, `02-minimal-operator.md`, `03-task-service.md`, `04-minimal-runtime.md`, `05-alertmanager-adapter.md`
