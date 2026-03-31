# KAPE Handler Runtime — Technical Design

**Status:** Draft
**Author:** Dzung Tran
**Session:** 4 — Handler Runtime Technical Design
**Created:** 2026-03-22
**Last Updated:** 2026-03-22 (rev 2)
**Depends on:** `kape-rfc.md`, `kape-crd-rfc.md`, `kape-open-questions.md`

---

## Table of Contents

1. [Overview](#1-overview)
2. [Pod Topology](#2-pod-topology)
3. [Layer 1 — Startup and Configuration](#3-layer-1--startup-and-configuration)
4. [Layer 2 — NATS Consumer Loop](#4-layer-2--nats-consumer-loop)
5. [Layer 3 — LangGraph Agent Graph](#5-layer-3--langgraph-agent-graph)
6. [Layer 4 — ActionsRouter](#6-layer-4--actionsrouter)
7. [Layer 5 — Task Record Persistence](#7-layer-5--task-record-persistence)
8. [Layer 6 — OTEL Tracing](#8-layer-6--otel-tracing)
9. [Layer 7 — Error Handling](#9-layer-7--error-handling)
10. [KapeTool Sidecar](#10-kapetool-sidecar)
11. [Data Models](#11-data-models)
12. [Dependency Summary](#12-dependency-summary)

---

## 1. Overview

Each `KapeHandler` CRD results in a Deployment managed by the KAPE operator. Every pod in that Deployment runs the **KAPE Handler Runtime** — a Python process that:

1. Loads its configuration from a mounted ConfigMap fully materialized by the operator
2. Connects to one or more `KapeTool` sidecar containers over localhost
3. Pulls events from a NATS JetStream consumer
4. Runs a LangGraph ReAct agent to reason over the event and produce a structured output
5. Executes deterministic post-decision actions via the ActionsRouter
6. Persists Task audit records via `kape-task-service` REST API

All execution is single-concurrency per pod. Horizontal throughput scaling is handled entirely by KEDA on NATS consumer lag — the runtime never manages parallelism internally.

**Design principle:** The handler runtime is a message processor only. It does not read Kubernetes CRDs, does not manage infrastructure, and does not hold database credentials. The operator owns all infrastructure concerns and fully materializes everything the runtime needs into a ConfigMap and env vars before the pod starts.

---

## 2. Pod Topology

Each handler pod contains one `kapehandler` container and one `kapetool` sidecar container per `KapeTool` referenced in `spec.tools[]`. The operator injects sidecar containers during Deployment reconciliation.

```
┌────────────────────────────────────────────────────────────────────┐
│  KapeHandler Pod                                                   │
│                                                                    │
│  ┌──────────────────────┐     ┌──────────────────────────────┐    │
│  │   kapehandler        │────▶│  kapetool-slack (sidecar)    │────┼──▶ Slack MCP Server
│  │   (Python runtime)   │     └──────────────────────────────┘    │
│  │                      │────▶┌──────────────────────────────┐    │
│  │                      │     │  kapetool-kubectl (sidecar)  │────┼──▶ kubectl MCP Server
│  └──────────────────────┘     └──────────────────────────────┘    │
└────────────────────────────────────────────────────────────────────┘
```

The `kapehandler` container communicates with each `kapetool` sidecar over localhost. Each sidecar enforces the policy defined in its `KapeTool` CRD before forwarding requests to the upstream MCP server.

---

## 3. Layer 1 — Startup and Configuration

### 3.1 Config Loading

The runtime uses **dynaconf** for configuration with the following priority chain (highest to lowest):

```
CLI flags > environment variables > ConfigMap volume file > defaults
```

The operator generates a dedicated ConfigMap by fully materializing the `KapeHandler` spec and mounts it into the pod at `/etc/kape/settings.toml`. The runtime never reads the `KapeHandler` CR directly — the operator is the sole consumer of Kubernetes CRDs. This is a deliberate separation of concerns: the operator manages infrastructure, the runtime processes messages.

Sensitive values (LLM API key, NATS credentials) are stored in Kubernetes Secrets and injected as env vars by the operator via `secretKeyRef`. No credentials appear in the ConfigMap.

Example mounted `settings.toml`:

```toml
[kape]
handler_name = "falco-remediation"
handler_namespace = "kape-system"
cluster_name = "prod-apse1"
dry_run = false
max_iterations = 25
schema_name = "falco-remediation-schema"
max_event_age_seconds = 300

[llm]
provider = "anthropic"
model = "claude-sonnet-4-20250514"
system_prompt = """
You are a security remediation agent for cluster {{ cluster_name }}.
Received event: {{ event.type }} from {{ event.source }} at {{ timestamp }}.
"""

[nats]
subject = "kape.events.security.falco"
consumer = "kape-falco-remediation"
stream = "kape-events"

[task_service]
endpoint = "http://kape-task-service.kape-system:8080"

[otel]
endpoint = "http://otel-collector.kape-system:4318"
service_name = "kape-handler"

[tools.slack]
sidecar_port = 8080
transport = "sse"

[tools.kubectl]
sidecar_port = 8081
transport = "sse"
```

Example env vars injected by operator:

```yaml
env:
  # LLM credentials
  - name: ANTHROPIC_API_KEY
    valueFrom:
      secretKeyRef:
        name: kape-llm-credentials
        key: api_key
  # NATS credentials — managed entirely by operator, not visible to engineer
  - name: NATS_CREDENTIALS
    valueFrom:
      secretKeyRef:
        name: kape-nats-credentials
        key: nats_creds
  # Engineer-defined envs from KapeHandler.spec.envs
  - name: WEBHOOK_URL
    value: "https://hooks.example.com/incident"
  - name: WEBHOOK_TOKEN
    valueFrom:
      secretKeyRef:
        name: webhook-credentials
        key: token
```

### 3.2 KapeHandler.spec.envs

`spec.envs` follows the same pattern as Pod and Deployment env configuration — supports literal values and `secretKeyRef` / `configMapKeyRef` references:

```yaml
spec:
  envs:
    - name: WEBHOOK_URL
      value: "https://hooks.example.com/incident"
    - name: WEBHOOK_TOKEN
      valueFrom:
        secretKeyRef:
          name: webhook-credentials
          key: token
```

The operator injects all `spec.envs` entries into the handler pod spec at Deployment reconciliation time. These env vars are available in action data templates via `{{ env.VAR_NAME }}`. Secrets are never hardcoded in the `KapeHandler` CR — they stay in Kubernetes Secrets and are only accessible at runtime via the env context.

### 3.3 Startup Sequence

```
1. Load config      (dynaconf: settings.toml → env vars → CLI flags)
2. Connect to KapeTool sidecars   (one per tools.* section in settings.toml)
3. Build LangGraph agent graph    (static after startup — no dynamic mutation)
4. Connect to NATS JetStream      (pull consumer, credentials from NATS_CREDENTIALS env)
5. Signal readiness               (FastAPI HTTP probe on :8080 → /readyz)
```

All steps must succeed before readiness is signalled. Failure at any step crashes the pod — Kubernetes restarts it. Startup errors are a Kubernetes operational concern handled via pod logs and events, not OTEL.

### 3.4 Readiness and Liveness

A FastAPI process on `:8080` exposes:

- `GET /healthz` — liveness probe. Returns `200` immediately after process start.
- `GET /readyz` — readiness probe. Returns `200` only after all startup steps complete. Returns `503` until then.

---

## 4. Layer 2 — NATS Consumer Loop

### 4.1 Consumer Model

The runtime uses a **NATS JetStream pull consumer**. Pull gives explicit flow control — the runtime fetches the next message only when it is ready to process it. This prevents message accumulation in-flight during scale-up events.

KEDA scales on NATS consumer lag independently of the runtime's internal processing. The runtime never manages internal concurrency — one event at a time per pod.

### 4.2 Consumer Loop

```
fetch next message (blocking pull)
        │
        ▼
ACK immediately
        │
        ▼
POST /tasks → Task{status: Processing, received_at: now()}
        │
        ▼
Parse CloudEvents envelope
├── malformed → PATCH /tasks/{id} {status: UnprocessableEvent} → next message
└── valid → continue
        │
        ▼
Staleness check (max_event_age_seconds from config)
├── stale → DELETE /tasks/{id} → next message   (no audit trail for stale drops)
└── fresh → continue
        │
        ▼
Invoke LangGraph agent (async)
        │
        ▼
PATCH /tasks/{id} {status: <final>, completed_at, duration_ms, ...}
        │
        ▼
fetch next message
```

### 4.3 Ack Strategy

The message is ACKed immediately on receipt — before any processing begins. This means:

- Other handler replicas cannot receive the same message
- If the pod crashes after ACK but before Task completion, the Task stays `Processing` permanently
- No automatic redelivery — the operator (human) decides via the dashboard

This is intentional. KAPE is audit-first — silent redelivery and duplicate executions are worse than a visible stuck Task.

### 4.4 Staleness Check

Evaluated inside the handler after the Task record is created. Stale events are silently dropped — the Task record is deleted, no audit trail is produced. Staleness is a pre-processing concern, not an execution outcome.

```python
age_seconds = (datetime.utcnow() - event.time).total_seconds()
if age_seconds > config.kape.max_event_age_seconds:
    await task_service.delete_task(task_id)
    return
```

---

## 5. Layer 3 — LangGraph Agent Graph

### 5.1 Graph Structure

```
[START]
   │
   ▼
[entry_router]       ← conditional: normal path or retry path
   │
   ├── ActionError retry → [route_actions]   (skip LLM, re-run failed actions only)
   │
   └── normal / full LLM retry →
         │
         ▼
      [reason]              ← ReAct loop: LLM call with tool bindings
         │
         ├── tool_calls → [call_tools] → back to [reason]
         │
         └── final answer →
               │
               ▼
            [parse_output]        ← model.with_structured_output(SchemaOutput)
               │
               ▼
            [validate_schema]     ← Pydantic assertion; writes Task on failure
               │
               ├── failed → [END]
               │
               └── passed →
                     │
                     ▼
                  [run_guardrails]     ← LangChain PII middleware + custom hooks
                     │
                     ▼
                  [route_actions]      ← ActionsRouter (deterministic)
                     │
                     ▼
                   [END]
```

### 5.2 Entry Router

The entry router checks the CloudEvent extension attribute `retry_of`. If present, it fetches the original Task from `kape-task-service` and routes based on `preRetryStatus`:

```python
async def entry_router(state: AgentState) -> str:
    retry_of = state["event"].extensions.get("retry_of")

    if not retry_of:
        return "reason"   # normal flow

    task = await task_service.get_task(retry_of)

    if task.status in (
        TaskStatus.Processing,
        TaskStatus.SchemaValidationFailed,
        TaskStatus.Failed,
        TaskStatus.Timeout,
    ):
        # Decision was invalid or state unknown — must re-run full LLM flow
        return "reason"

    if task.status == TaskStatus.ActionError:
        # Decision was valid — skip LLM, retry only failed actions
        state["retry_task"] = task
        return "route_actions"
```

**preRetryStatus routing:**

| Original Task Status     | LLM needed? | Reason                                                       |
| ------------------------ | ----------- | ------------------------------------------------------------ |
| `Processing`             | Yes         | Unknown state — pod may have crashed mid-reasoning           |
| `SchemaValidationFailed` | Yes         | LLM output was invalid — must re-reason                      |
| `Failed`                 | Yes         | Unhandled error — cause unknown, safest to re-run everything |
| `Timeout`                | Yes         | Unknown state — operator judged it stuck                     |
| `ActionError`            | No          | Decision was valid — only actions failed                     |

### 5.3 AgentState

```python
class AgentState(TypedDict):
    # Input
    event:          CloudEvent
    task_id:        str
    retry_task:     Task | None      # populated on ActionError retry path

    # Reasoning
    messages:       list[BaseMessage]

    # Output
    schema_output:  dict | None      # validated output against KapeSchema
    parse_error:    str | None

    # Actions
    action_results: list[ActionResult]
    task_status:    TaskStatus

    # Control
    should_abort:   bool
    dry_run:        bool
```

### 5.4 Reason Node

The `reason` node is the ReAct loop core. The system prompt is a Jinja2 template defined in `spec.llmConfig.systemPrompt` (materialized into the ConfigMap) and rendered at event ingestion time.

**Jinja2 render context:**

```python
context = {
    "handler_name":  config.kape.handler_name,
    "cluster_name":  config.kape.cluster_name,
    "namespace":     config.kape.handler_namespace,
    "timestamp":     datetime.utcnow().isoformat(),
    "event":         event.model_dump(),
    "env":           dict(os.environ),   # all injected envs including spec.envs
}
```

**Max iterations:** `spec.maxIterations` (default `50` from `kape-config`, overridable per handler). Exceeding the limit writes `Task{status: Failed, error.type: MaxIterationsExceeded}` and terminates.

### 5.5 Call Tools Node

Tool calls are dispatched to the appropriate `KapeTool` sidecar over localhost HTTP. W3C TraceContext headers are injected into every outgoing request for end-to-end trace propagation.

```python
async def call_tools(state: AgentState) -> AgentState:
    tool_results = []
    for tool_call in state["messages"][-1].tool_calls:
        result = await execute_tool_via_sidecar(tool_call)
        tool_results.append(ToolMessage(content=result, tool_call_id=tool_call["id"]))
    return {"messages": state["messages"] + tool_results}
```

### 5.6 Parse Output Node

Uses LangChain's `.with_structured_output()` API. The `SchemaOutput` Pydantic model is generated at startup from the `KapeSchema` spec materialized in the ConfigMap. No extra dependencies beyond LangChain.

```python
structured_model = model.with_structured_output(SchemaOutput)

async def parse_output(state: AgentState) -> AgentState:
    try:
        output: SchemaOutput = await structured_model.ainvoke(state["messages"])
        return {"schema_output": output.model_dump(), "parse_error": None}
    except ValidationError as e:
        return {"schema_output": None, "parse_error": str(e)}
```

Fail-fast on validation failure. No automatic retry. The failed Task record is the signal — the engineer inspects, fixes the `KapeSchema` or system prompt, redeploys via GitOps, and retries via the dashboard.

### 5.7 Validate Schema Node

Explicit audit checkpoint that runs after `parse_output`. Exists to produce a visible Task record on schema failure.

```python
async def validate_schema(state: AgentState) -> AgentState:
    if state["parse_error"]:
        await task_service.update_task(
            state["task_id"],
            status=TaskStatus.SchemaValidationFailed,
            error=TaskError(
                type="SchemaValidationFailed",
                detail=state["parse_error"],
                schema=config.kape.schema_name,
            ),
        )
        return {"should_abort": True}
    return {"should_abort": False}
```

### 5.8 Run Guardrails Node

Implemented as LangChain middleware. Two layers:

**Layer 1 — PIIMiddleware (built-in LangChain)**

Applied at the agent level across all LLM input and output:

```python
middleware = [
    PIIMiddleware("email",       strategy="redact", apply_to_input=True, apply_to_output=True),
    PIIMiddleware("api_key",     strategy="block",  apply_to_input=True),
    PIIMiddleware("credit_card", strategy="redact", apply_to_input=True, apply_to_output=True),
]
```

**Layer 2 — Custom `before_agent` / `after_agent` hooks**

Engineer-configurable deterministic checks materialized from `KapeHandler.spec.guardrails` into the ConfigMap by the operator. Run before and after agent execution for session-level data safety checks.

Tool-level access control (which tools the LLM can call) is enforced entirely by the `KapeTool` sidecar allowlist — not by the guardrails layer.

---

## 6. Layer 4 — ActionsRouter

### 6.1 Design Principles

The ActionsRouter is fully deterministic — no LLM involvement. It receives the validated `schema_output` and executes the `actions[]` array declared within it. This is a programmable dispatch table, not an AI decision.

All eligible actions execute in parallel via `asyncio.gather`. Failure of one action does not block others.

### 6.2 Action Schema

Each action in `schema_output.actions[]`:

```yaml
actions:
  - name: "alert-security-team"
    condition: "decision.severity == 'critical'"
    type: "event-emitter"
    data:
      subject: "kape.events.security.alert"
      payload:
        severity: "{{ decision.severity }}"
        resource: "{{ decision.resource }}"

  - name: "store-incident-context"
    condition: "true"
    type: "save-memory"
    data:
      collection: "incidents"
      content: "{{ decision.summary }}"
      metadata:
        event_id: "{{ event.id }}"

  - name: "notify-external-system"
    condition: "decision.notify == true"
    type: "webhook"
    data:
      url: "{{ env.WEBHOOK_URL }}"
      method: "POST"
      headers:
        Authorization: "Bearer {{ env.WEBHOOK_TOKEN }}"
      body:
        incident: "{{ decision.summary }}"
```

### 6.3 Action Types

| Type            | Description                            |
| --------------- | -------------------------------------- |
| `event-emitter` | Publish a CloudEvent to a NATS subject |
| `save-memory`   | Write to Qdrant vector store           |
| `webhook`       | Call an external HTTP endpoint         |

### 6.4 Condition Evaluation

Conditions are evaluated using `simpleeval` — never raw `eval()`:

```python
from simpleeval import simple_eval

context = {
    "decision": schema_output,
    "event":    event.model_dump(),
    "env":      dict(os.environ),
}

eligible = [
    action for action in actions
    if simple_eval(action.condition, names=context)
]
```

### 6.5 Data Templating

`action.data` fields support Jinja2 templating. The same context object is used for both condition evaluation and template rendering — including `env` for all injected env vars from `spec.envs`:

```python
from jinja2 import Environment

jinja_env = Environment()

def render_action_data(data: dict, context: dict) -> dict:
    rendered = {}
    for key, value in data.items():
        if isinstance(value, str):
            rendered[key] = jinja_env.from_string(value).render(context)
        elif isinstance(value, dict):
            rendered[key] = render_action_data(value, context)
        else:
            rendered[key] = value
    return rendered
```

### 6.6 Execution

```python
async def route_actions(state: AgentState) -> AgentState:
    actions = get_eligible_actions(state)

    # On ActionError retry: skip previously succeeded actions
    if state.get("retry_task"):
        succeeded = {r.name for r in state["retry_task"].actions
                     if r.status == "Completed"}
        actions = [a for a in actions if a.name not in succeeded]

    if state["dry_run"]:
        results = [
            ActionResult(name=a.name, type=a.type, status="Skipped", dry_run=True)
            for a in actions
        ]
        return {"action_results": results, "task_status": TaskStatus.Completed}

    rendered = [render_action_data(a, build_context(state)) for a in actions]
    outcomes = await asyncio.gather(
        *[ACTION_REGISTRY[a.type].execute(a) for a in rendered],
        return_exceptions=True,
    )

    results = [
        ActionResult(
            name=action.name,
            type=action.type,
            status="Failed" if isinstance(outcome, Exception) else "Completed",
            error=str(outcome) if isinstance(outcome, Exception) else None,
            dry_run=False,
        )
        for action, outcome in zip(rendered, outcomes)
    ]

    if all(r.status == "Failed" for r in results):
        overall = TaskStatus.Failed
    elif any(r.status == "Failed" for r in results):
        overall = TaskStatus.ActionError
    else:
        overall = TaskStatus.Completed

    return {"action_results": results, "task_status": overall}
```

### 6.7 DryRun Behaviour

`dryRun` is a property of `KapeHandler.spec.dryRun`. When true:

- The full agent loop executes — LLM calls, tool calls, schema validation, guardrails all run normally
- The ActionsRouter evaluates conditions and renders templates but skips all execution
- Task is written with `status: Completed, dry_run: true` and full `action_results[]` showing what would have executed
- Engineers use dryRun to validate prompts and schema outputs against real events without side effects

---

## 7. Layer 5 — Task Record Persistence

### 7.1 Storage and Access

PostgreSQL only. All Task persistence is mediated entirely by `kape-task-service` — a Go REST API. The handler runtime never holds database credentials and never connects to PostgreSQL directly. All reads and writes go through HTTP calls to `kape-task-service`, whose endpoint is injected into the handler ConfigMap by the operator.

### 7.2 Task Service API (handler-facing endpoints)

| Method   | Path          | Description                                 |
| -------- | ------------- | ------------------------------------------- |
| `POST`   | `/tasks`      | Create Task on ACK                          |
| `PATCH`  | `/tasks/{id}` | Update Task to final status                 |
| `DELETE` | `/tasks/{id}` | Delete Task on stale event drop             |
| `GET`    | `/tasks/{id}` | Fetch original Task on retry (entry router) |

### 7.3 Write Pattern

Two writes per event:

```
1. On ACK receipt:
   POST /tasks
   → Task{status: Processing, received_at: now()}

2. On agent completion:
   PATCH /tasks/{id}
   → {status, schema_output, actions, error, completed_at, duration_ms, dry_run, otel_trace_id}
```

### 7.4 Task Schema

```sql
CREATE TABLE tasks (
    id              TEXT PRIMARY KEY,       -- ULID, sortable by time
    cluster         TEXT NOT NULL,
    handler         TEXT NOT NULL,
    namespace       TEXT NOT NULL,
    event_id        TEXT NOT NULL,
    event_source    TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    status          TEXT NOT NULL,
    dry_run         BOOLEAN NOT NULL DEFAULT false,
    schema_output   JSONB,
    actions         JSONB,                  -- list[ActionResult]
    error           JSONB,                  -- TaskError | null
    retry_of        TEXT REFERENCES tasks(id),
    otel_trace_id   TEXT,
    received_at     TIMESTAMPTZ NOT NULL,
    completed_at    TIMESTAMPTZ,
    duration_ms     INTEGER
);
```

### 7.5 Task Status Enum

| Status                   | Description                                                                    |
| ------------------------ | ------------------------------------------------------------------------------ |
| `Processing`             | ACK received, agent running. Pod may be alive or crashed — black box.          |
| `Completed`              | All actions succeeded (or `dry_run: true`).                                    |
| `Failed`                 | Unhandled runtime exception, or max iterations exceeded.                       |
| `SchemaValidationFailed` | LLM output did not match `KapeSchema`.                                         |
| `ActionError`            | One or more actions failed in the ActionsRouter.                               |
| `UnprocessableEvent`     | CloudEvent envelope was malformed — could not parse.                           |
| `PendingApproval`        | `event-emitter` action published to approval subject; awaiting human approval. |
| `Timeout`                | Manually marked via dashboard — operator judged task stuck.                    |
| `Retried`                | Superseded by a retry execution. Original task preserved for lineage.          |

### 7.6 Timeout Detection

Timeout is a UI concern — no background jobs. The dashboard computes `elapsed = now() - received_at` for every `Processing` task and renders it live. The operator decides when elapsed time indicates a stuck task and manually marks it `Timeout` via the dashboard.

`kape-task-service` exposes:

- `PATCH /tasks/{id}/status` — mark single task as `Timeout`
- `PATCH /tasks/bulk/status` — mark multiple tasks as `Timeout`

### 7.7 Retry Flow

```
1. Operator marks Task as Timeout (if Processing) or clicks Retry (any retryable status)
2. Dashboard calls POST /tasks/{id}/retry on kape-task-service
3. Service fetches original Task from PostgreSQL
4. Service PATCH /tasks/{id} → {status: Retried}
5. Service re-publishes original CloudEvent to NATS with CloudEvent extension:
     retry_of: <original_task_id>
6. Handler receives event, entry_router fetches original Task via GET /tasks/{retry_of}
7. Routes based on preRetryStatus (see Section 5.2)
8. New Task created with retry_of: <original_task_id>
```

`retry_of` linkage is always written on the new Task — regardless of retry scenario. Full chain visibility in the dashboard.

**Retryable statuses and routing:**

| Status                   | LLM path                       |
| ------------------------ | ------------------------------ |
| `Processing`             | Full LLM                       |
| `SchemaValidationFailed` | Full LLM                       |
| `Failed`                 | Full LLM                       |
| `Timeout`                | Full LLM                       |
| `ActionError`            | Skip LLM — failed actions only |

---

## 8. Layer 6 — OTEL Tracing

### 8.1 Instrumentation Strategy

OTEL scope is strictly KAPE business logic — event processing, LLM calls, tool calls, action execution. Kubernetes operational concerns (pod crash, eviction, startup failure) are handled via standard k8s tooling. OTEL is not used for infrastructure-level signals.

**Instrumentation library:** `openinference-instrumentation-langchain` — decided in the main RFC. OpenInference provides auto-instrumentation for all LangGraph nodes, LLM calls, and tool invocations following OpenInference semantic conventions. No LangSmith dependency. Backend-agnostic — the OTLP endpoint is a configuration concern.

### 8.2 Tracer Setup

Configured at startup, before the LangGraph graph is built:

```python
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.resources import Resource
from opentelemetry import trace
from openinference.instrumentation.langchain import LangChainInstrumentor

resource = Resource.create({
    "service.name":   "kape-handler",
    "kape.handler":   config.kape.handler_name,
    "kape.cluster":   config.kape.cluster_name,
    "kape.namespace": config.kape.handler_namespace,
})

provider = TracerProvider(resource=resource)
provider.add_span_processor(BatchSpanProcessor(
    OTLPSpanExporter(endpoint=config.otel.endpoint)
))
trace.set_tracer_provider(provider)

LangChainInstrumentor().instrument()
```

### 8.3 Span Structure

```
trace: kape.handler.process_event
│   kape.handler    = falco-remediation
│   kape.cluster    = prod-apse1
│   kape.event_id   = 01HX...
│   kape.event_type = falco.alert
│   kape.task_id    = 01JK...
│   kape.dry_run    = false
│
├── [auto] LangGraph.reason              (LLM call, iterations, token counts)
│     └── [auto] LangGraph.tool_call     (per MCP tool invocation)
│           └── [manual] kape.sidecar.call  (sidecar → upstream MCP)
│
├── [auto] LangGraph.parse_output
├── [auto] LangGraph.validate_schema
├── [auto] LangGraph.run_guardrails
│
└── [manual] kape.route_actions
      ├── [manual] kape.action.{name}    (per action: type, status, duration)
      └── ...
```

The root span's `trace_id` is stored as `otel_trace_id` on the Task record.

### 8.4 KapeTool Sidecar Trace Propagation

The handler injects W3C TraceContext headers into every HTTP request to the sidecar. The sidecar extracts the context and creates child spans under the same trace.

**Handler side:**

```python
from opentelemetry.propagate import inject
from opentelemetry import trace

tracer = trace.get_tracer("kape.handler")

async def execute_tool_via_sidecar(tool_call, sidecar_url):
    with tracer.start_as_current_span("kape.sidecar.call") as span:
        span.set_attribute("tool.name", tool_call["name"])
        headers = {}
        inject(headers)
        async with httpx.AsyncClient() as client:
            return await client.post(
                f"{sidecar_url}/tools/call",
                json=tool_call,
                headers=headers,
            )
```

**Sidecar side:**

```python
from opentelemetry.propagate import extract

@app.post("/tools/call")
async def handle(request: Request, body: ToolCallRequest):
    ctx = extract(dict(request.headers))
    with tracer.start_as_current_span("kapetool.handle", context=ctx) as span:
        span.set_attribute("tool.name", body.tool)
        with tracer.start_as_current_span("kapetool.policy_check"): ...
        with tracer.start_as_current_span("kapetool.audit_log"): ...
        with tracer.start_as_current_span("kapetool.upstream_mcp_call"): ...
```

---

## 9. Layer 7 — Error Handling

### 9.1 Error Categories

**Category 1 — Expected errors (handled by graph nodes)**

| Error                                | Node              | Task Status              |
| ------------------------------------ | ----------------- | ------------------------ |
| LLM output fails Pydantic validation | `validate_schema` | `SchemaValidationFailed` |
| One or more actions fail             | `route_actions`   | `ActionError`            |
| All actions fail                     | `route_actions`   | `Failed`                 |
| Max iterations exceeded              | `reason` loop     | `Failed`                 |

**Category 2 — Unhandled runtime errors**

Exceptions escaping the graph are caught by the consumer loop wrapper:

```python
async def consume_loop():
    async for msg in consumer:
        await msg.ack()
        task_id = await task_service.create_task(status=TaskStatus.Processing, ...)
        try:
            await graph.ainvoke(build_state(msg, task_id))
        except Exception as e:
            await task_service.update_task(
                task_id,
                status=TaskStatus.Failed,
                error=TaskError(
                    type="UnhandledError",
                    detail=str(e),
                    traceback=traceback.format_exc(),
                ),
            )
```

Every event is guaranteed a terminal Task record, even on unexpected failure.

**Category 3 — Pod-level errors**

The pod dies before the `except` block runs. Task stays `Processing` permanently. The dashboard displays elapsed time — the operator marks it `Timeout` and retries.

**Category 4 — Malformed CloudEvent envelope**

```python
await task_service.update_task(
    task_id,
    status=TaskStatus.UnprocessableEvent,
    error=TaskError(
        type="MalformedEvent",
        detail="Could not parse CloudEvents envelope",
        raw=msg.data.decode("utf-8", errors="replace"),
    ),
)
```

### 9.2 No Automatic Retry

There is no automatic retry anywhere in the runtime. LLM calls are expensive — automatic retry on failure silently burns token budget. All retry decisions are made explicitly by the operator via the dashboard.

---

## 10. KapeTool Sidecar

### 10.1 Responsibilities

Each `kapetool` sidecar container:

1. Exposes an MCP proxy over **SSE** (`:8080`) and **Streamable HTTP** (`:8081`)
2. Enforces `KapeTool.spec.allowedTools` whitelist (exact string + glob matching)
3. Applies input/output redaction rules from `KapeTool.spec.redaction`
4. Writes a structured audit log entry per tool call via `kape-task-service`
5. Forwards allowed, redacted requests to the upstream MCP server

### 10.2 Language and Stack

Python — consistent with the handler runtime. Uses the `mcp` PyPI package which supports both SSE and Streamable HTTP transports natively.

### 10.3 Allowlist Enforcement

```python
import fnmatch

def is_allowed(tool_name: str, allowed_tools: list[str]) -> bool:
    return any(fnmatch.fnmatch(tool_name, pattern) for pattern in allowed_tools)
```

Denied tool calls return a structured MCP error response. The handler runtime treats this as a tool call failure — the LLM sees the denial and may reason around it.

### 10.4 Redaction

Input and output fields are redacted using jsonPath rules before audit log write and before forwarding upstream. Redacted fields are replaced with `"[REDACTED]"`.

```yaml
redaction:
  input:
    - jsonPath: "$.token"
    - jsonPath: "$.credentials"
  output:
    - jsonPath: "$.email"
    - jsonPath: "$.phoneNumber"
```

### 10.5 Audit Log

Every tool call writes an entry via `kape-task-service` (persisted to `tool_audit_log` in PostgreSQL):

```sql
CREATE TABLE tool_audit_log (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL,
    handler     TEXT NOT NULL,
    tool_name   TEXT NOT NULL,
    kapetool    TEXT NOT NULL,
    input       JSONB,
    output      JSONB,
    status      TEXT NOT NULL,    -- allowed | denied | error
    deny_reason TEXT,
    duration_ms INTEGER,
    called_at   TIMESTAMPTZ NOT NULL
);
```

### 10.6 KapeTool CRD Sidecar Fields

```yaml
apiVersion: kape.io/v1alpha1
kind: KapeTool
metadata:
  name: slack-mcp
spec:
  upstream:
    transport: sse
    url: http://slack-mcp-server:8080

  allowedTools:
    - "slack_post_message"
    - "slack_list_channels"
    - "slack_*"

  redaction:
    input:
      - jsonPath: "$.token"
    output:
      - jsonPath: "$.email"

  audit:
    enabled: true
```

---

## 11. Data Models

### 11.1 TaskStatus

```python
class TaskStatus(str, Enum):
    Processing             = "Processing"
    Completed              = "Completed"
    Failed                 = "Failed"
    SchemaValidationFailed = "SchemaValidationFailed"
    ActionError            = "ActionError"
    UnprocessableEvent     = "UnprocessableEvent"
    PendingApproval        = "PendingApproval"
    Timeout                = "Timeout"
    Retried                = "Retried"
```

### 11.2 Task

```python
class Task(BaseModel):
    id:             str
    cluster:        str
    handler:        str
    namespace:      str
    event_id:       str
    event_source:   str
    event_type:     str
    status:         TaskStatus
    dry_run:        bool
    schema_output:  dict | None
    actions:        list[ActionResult]
    error:          TaskError | None
    retry_of:       str | None          # original Task ID — always set on retries
    otel_trace_id:  str | None
    received_at:    datetime
    completed_at:   datetime | None
    duration_ms:    int | None
```

### 11.3 TaskError

```python
class TaskError(BaseModel):
    type:       str         # SchemaValidationFailed | UnhandledError | MalformedEvent | MaxIterationsExceeded
    detail:     str
    schema:     str | None  # KapeSchema name (SchemaValidationFailed only)
    raw:        str | None  # raw event bytes (MalformedEvent only)
    traceback:  str | None  # Python traceback (UnhandledError only)
```

### 11.4 ActionResult

```python
class ActionResult(BaseModel):
    name:    str
    type:    str     # event-emitter | save-memory | webhook
    status:  str     # Completed | Failed | Skipped
    dry_run: bool
    error:   str | None
```

---

## 12. Dependency Summary

| Package                                   | Purpose                                               |
| ----------------------------------------- | ----------------------------------------------------- |
| `langgraph`                               | Agent graph execution                                 |
| `langchain-anthropic`                     | Anthropic LLM integration                             |
| `langchain-mcp-adapters`                  | MCP tool integration in ReAct loop                    |
| `langchain`                               | Middleware (PII), structured output API               |
| `openinference-instrumentation-langchain` | OTEL auto-instrumentation (RFC decision)              |
| `opentelemetry-exporter-otlp-proto-http`  | OTLP HTTP exporter                                    |
| `nats-py`                                 | NATS JetStream pull consumer                          |
| `pydantic`                                | Schema validation, data models                        |
| `dynaconf`                                | Config loading (flag-first priority chain)            |
| `jinja2`                                  | System prompt + action data templating                |
| `simpleeval`                              | Safe condition expression evaluation                  |
| `fastapi`                                 | Readiness/liveness HTTP probe                         |
| `httpx`                                   | Async HTTP client (sidecar calls, task-service calls) |
| `python-ulid`                             | ULID generation for Task IDs                          |
| `mcp`                                     | KapeTool sidecar MCP proxy (SSE + Streamable HTTP)    |
