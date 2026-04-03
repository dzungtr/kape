# KAPE Audit Database — Technical Design

**Status:** Draft
**Author:** Dzung Tran
**Session:** 8 — Audit Database and Task Record Schema
**Created:** 2026-04-03
**Depends on:** `kape-rfc.md`, `kape-handler-runtime-design.md`, `kape-operator-design.md`

---

## Table of Contents

1. [Overview](#1-overview)
2. [Database Technology Decision](#2-database-technology-decision)
3. [Schema Design](#3-schema-design)
4. [Task Lifecycle State Machine](#4-task-lifecycle-state-machine)
5. [Partition Management](#5-partition-management)
6. [Dashboard Query Patterns](#6-dashboard-query-patterns)
7. [Observability Boundary](#7-observability-boundary)

---

## 1. Overview

The KAPE audit database is the persistent store for all Task execution records. It provides a complete, immutable audit trail of every event processed by every handler — what event triggered execution, what decision the LLM made, what actions ran, and what the outcome was.

**Design principle:** The audit database is an audit store, not a metrics store. Handler health aggregates (throughput, latency p99, failure rate) are owned by Prometheus and the OTEL backend. PostgreSQL owns Task lifecycle and decision records only.

**Access model:** All reads and writes are mediated exclusively by `kape-task-service` — a Go REST API. No other component (handler runtime, operator, dashboard) connects to PostgreSQL directly. Handler pods write via HTTP. The dashboard reads via HTTP. `kape-task-service` uses an ORM internally; SQL queries are not pre-specified in this document.

**Tool call audit:** Tool call detail (every MCP call during the ReAct loop — inputs, outputs, latency, allow/deny outcomes) is owned by the OTEL backend via `openinference-instrumentation-langchain`. A `task_id` span attribute is set on the root trace, enabling the dashboard to deep-link from a Task record to its full trace. No `tool_audit_log` table is maintained in PostgreSQL.

---

## 2. Database Technology Decision

**Decision: PostgreSQL via CloudNativePG.**

### Rationale

KAPE's access pattern is mixed — not pure append-only time-series:

- **Point lookups by task ID** — retry entry router, dashboard drill-down, trace linking
- **Filtered list queries** — live feed by handler/status, timeout management view
- **JSONB column access** — `schema_output`, `actions`, `event_raw`, `error`
- **Aggregate queries** — decision distribution over a time window

PostgreSQL covers all of these well. ClickHouse would provide better aggregate performance but degrades point lookup performance — the wrong tradeoff given that the retry flow requires a single-row fetch by task ID. A second database operator dependency is also unjustified at KAPE's expected write volume (events per minute per handler, not events per second).

**CloudNativePG** is the Kubernetes operator for PostgreSQL. It is production-grade, widely adopted, and consistent with the Kubernetes-native mandate of the platform. The `kape-task-service` deployment includes a `CloudNativePG Cluster` manifest in the Helm chart.

---

## 3. Schema Design

### 3.1 `task_status` Enum

```sql
CREATE TYPE task_status AS ENUM (
    'Processing',
    'Completed',
    'Failed',
    'SchemaValidationFailed',
    'ActionError',
    'UnprocessableEvent',
    'PendingApproval',
    'Timeout',
    'Retried'
);
```

`PendingApproval` is included in v1 schema to avoid a migration when approval flows ship in v2. The handler runtime never writes this value in v1.

### 3.2 `tasks` Table

```sql
CREATE TABLE tasks (

    -- Identity
    id              TEXT        PRIMARY KEY,          -- ULID, time-sortable
    cluster         TEXT        NOT NULL,
    handler         TEXT        NOT NULL,
    namespace       TEXT        NOT NULL,

    -- Event provenance
    event_id        TEXT        NOT NULL,             -- CloudEvents id field
    event_source    TEXT        NOT NULL,             -- CloudEvents source field
    event_type      TEXT        NOT NULL,             -- CloudEvents type field
    event_raw       JSONB       NOT NULL,             -- full CloudEvents envelope, immutable

    -- Execution
    status          task_status NOT NULL,
    dry_run         BOOLEAN     NOT NULL DEFAULT false,

    -- Output
    schema_output   JSONB,                            -- validated KapeSchema output; null until agent completes
    actions         JSONB,                            -- list[ActionResult]; null until route_actions runs

    -- Error
    error           JSONB,                            -- TaskError | null

    -- Lineage
    retry_of        TEXT        REFERENCES tasks(id), -- FK to original Task; always set on retries

    -- Observability
    otel_trace_id   TEXT,                             -- deep link to OTEL backend; consumable property only

    -- Timing
    received_at     TIMESTAMPTZ NOT NULL,             -- when NATS ACK was sent
    completed_at    TIMESTAMPTZ,                      -- when final PATCH was written by handler
    duration_ms     INTEGER                           -- computed: completed_at - received_at

) PARTITION BY RANGE (received_at);
```

### 3.3 Column Notes

**`id` (ULID):** Time-sortable, globally unique. Generated by the handler runtime at Task creation. Enables `ORDER BY id` as a proxy for `ORDER BY received_at` when needed.

**`event_raw` (JSONB, NOT NULL, immutable):** The full CloudEvents envelope stored permanently at Task creation time. Required for the retry flow — `kape-task-service` re-publishes this verbatim to NATS when an operator retries a Task. Also used by the dashboard drill-down to display the triggering event without an OTEL lookup. Never updated after initial write.

**`schema_output` (JSONB, nullable):** The validated structured output from the LLM, conforming to the referenced `KapeSchema`. Null for Tasks that terminate before the `parse_output` node (e.g., `UnprocessableEvent`, `Processing`).

**`actions` (JSONB, nullable):** A list of `ActionResult` objects from the ActionsRouter. Each entry records the action name, type, outcome status, and any error detail. Null until `route_actions` runs.

**`error` (JSONB, nullable):** A `TaskError` object present on any non-`Completed` terminal status. Contains `type`, `detail`, and optional fields (`schema`, `raw`, `traceback`) depending on error category.

**`retry_of` (FK, nullable):** Set on every retry execution, pointing to the original Task ID. Enables the dashboard to walk the full retry lineage chain. The original Task is marked `Retried` when a retry is initiated — it is never deleted.

**`otel_trace_id` (TEXT, nullable):** The root span trace ID, stored as a consumable property for deep linking to the OTEL backend. Not indexed — no cross-task queries against this column.

**`llm_prompt` / `llm_response`:** Excluded. These are owned by the OTEL trace. Storing them in PostgreSQL would double PII exposure surface with no additional query value.

### 3.4 Indexes

```sql
-- Live feed: time-range scans across all handlers
CREATE INDEX idx_tasks_received_at
    ON tasks (received_at DESC);

-- Per-handler filtering: live feed and handler-scoped views
CREATE INDEX idx_tasks_handler
    ON tasks (handler, received_at DESC);

-- Status filtering: timeout management, failed task views
CREATE INDEX idx_tasks_status
    ON tasks (status, received_at DESC);

-- Lineage: find all retries of a given task (sparse — most tasks have no retries)
CREATE INDEX idx_tasks_retry_of
    ON tasks (retry_of)
    WHERE retry_of IS NOT NULL;
```

**Excluded indexes:**

- `otel_trace_id` — dashboard renders it as a deep link only; no query against it
- `schema_output` GIN — decision distribution queries run against a short time window on an already-filtered result set; acceptable without a GIN index at KAPE's volume

### 3.5 JSONB Schemas

**`actions` column — `list[ActionResult]`:**

```json
[
  {
    "name": "request-gitops-pr",
    "type": "event-emitter",
    "status": "Completed",
    "dry_run": false,
    "error": null
  },
  {
    "name": "notify-slack",
    "type": "webhook",
    "status": "Failed",
    "dry_run": false,
    "error": "connection timeout after 5s"
  }
]
```

**`error` column — `TaskError`:**

```json
{
  "type": "SchemaValidationFailed",
  "detail": "Field 'confidence' must be between 0 and 1; got 1.4",
  "schema": "karpenter-decision-schema",
  "raw": null,
  "traceback": null
}
```

`type` values: `SchemaValidationFailed` | `UnhandledError` | `MalformedEvent` | `MaxIterationsExceeded`

---

## 4. Task Lifecycle State Machine

### 4.1 States

| Status                   | Terminal | Description                                                                                   |
| ------------------------ | -------- | --------------------------------------------------------------------------------------------- |
| `Processing`             | No       | ACK received; agent running. Pod may be alive or crashed — black box until operator inspects. |
| `Completed`              | Yes      | All actions succeeded, or `dry_run: true`.                                                    |
| `Failed`                 | Yes      | Unhandled runtime exception, or max iterations exceeded.                                      |
| `SchemaValidationFailed` | Yes      | LLM output did not match the referenced `KapeSchema`.                                         |
| `ActionError`            | Yes      | One or more actions failed in the ActionsRouter; at least one succeeded.                      |
| `UnprocessableEvent`     | Yes      | CloudEvent envelope was malformed — could not parse.                                          |
| `PendingApproval`        | No       | Approval event published; awaiting human decision. (v2 only — not written in v1.)             |
| `Timeout`                | Yes      | Manually marked by operator via dashboard.                                                    |
| `Retried`                | Yes      | Superseded by a retry execution. Original Task preserved for lineage.                         |

`Processing` and `PendingApproval` are the only non-terminal states. All other states are terminal — once written, they are never updated.

### 4.2 Transition Map

```
[POST /tasks]
      │
      ▼
 Processing ──────────────────────────────────────────────────────────┐
      │                                                               │
      ├── malformed envelope                                          │
      │         └──► UnprocessableEvent  (terminal)                  │
      │                                                               │
      ├── stale event (age > maxEventAgeSeconds)                      │
      │         └──► [DELETE /tasks/{id}]  (no terminal record)       │
      │                                                               │
      ├── unhandled runtime exception                                 │
      │         └──► Failed  (terminal)                               │
      │                                                               │
      ├── max iterations exceeded                                     │
      │         └──► Failed  (terminal)                               │
      │                                                               │
      ├── schema validation fails                                     │
      │         └──► SchemaValidationFailed  (terminal)               │
      │                                                               │
      ├── all actions succeed (or dry_run: true)                      │
      │         └──► Completed  (terminal)                            │
      │                                                               │
      ├── some actions fail                                           │
      │         └──► ActionError  (terminal)                          │
      │                                                               │
      └── all actions fail                                            │
                └──► Failed  (terminal)                               │
                                                                      │
[PATCH /tasks/{id}/status — operator via dashboard]                   │
      │                                                               │
      └──► Timeout  (terminal) ◄─────────────────────────────────────┘

[POST /tasks/{id}/retry — operator via dashboard]
      │
      ├── marks original Task → Retried
      └── re-publishes event_raw to NATS with retry_of extension
            └──► new Task created with retry_of: <original_id>
```

### 4.3 Stale Event Handling

Stale events are dropped silently — the Task record is deleted via `DELETE /tasks/{id}`. No terminal record is produced. Staleness is a pre-processing discard, not an execution outcome. The handler evaluates staleness after Task creation but before agent invocation.

### 4.4 Retry Routing

When a retry is initiated, the `entry_router` node fetches the original Task and routes based on `preRetryStatus`:

| Original Status          | LLM re-runs? | Reason                                              |
| ------------------------ | ------------ | --------------------------------------------------- |
| `Processing`             | Yes          | Unknown state — pod may have crashed mid-reasoning  |
| `SchemaValidationFailed` | Yes          | LLM output was invalid — must re-reason             |
| `Failed`                 | Yes          | Cause unknown — safest to re-run everything         |
| `Timeout`                | Yes          | Unknown state — operator judged it stuck            |
| `ActionError`            | No           | Decision was valid — only failed actions are re-run |

On `ActionError` retry, the ActionsRouter skips actions whose `status == "Completed"` in the original Task's `actions` JSONB. Only failed actions are re-executed.

---

## 5. Partition Management

### 5.1 Partitioning Strategy

`tasks` is partitioned by month on `received_at`. This gives:

- Predictable partition sizes at KAPE's write volume
- Fast time-range scans via partition pruning (dashboard queries always filter by `received_at`)
- Simple retention management — drop a partition to expire old data (`DROP TABLE tasks_YYYY_MM`)

### 5.2 Partition Creation

Monthly partitions are created by `kape-task-service` on the first write that would land in a new partition, or pre-created by a Kubernetes `CronJob` in the Helm chart. The Helm chart ships with partitions for the current and next month pre-created at install time.

```sql
-- Example partition declarations
CREATE TABLE tasks_2026_04 PARTITION OF tasks
    FOR VALUES FROM ('2026-04-01') TO ('2026-05-01');

CREATE TABLE tasks_2026_05 PARTITION OF tasks
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
```

### 5.3 Retention

Retention is configured per deployment. Dropping a partition is a metadata operation — no row-level DELETE scan. A `CronJob` in `kape-system` runs monthly to drop partitions older than the configured retention window (default: 90 days).

```sql
-- Drop a partition (all rows and indexes dropped atomically)
DROP TABLE tasks_2026_01;
```

---

## 6. Dashboard Query Patterns

All queries are implemented in `kape-task-service` via ORM. The following describes the access patterns — not the SQL. `kape-task-service` exposes REST endpoints; the dashboard never queries PostgreSQL directly.

### Query 1 — Live Task Feed

**Use case:** Primary dashboard view. Recent tasks filterable by handler and status.

**Access pattern:** Filter by `received_at` window, optional `handler` and `status` filters, ordered by `received_at DESC`, paginated.

**Indexes used:** `idx_tasks_handler` (with handler filter) or `idx_tasks_received_at` (without).

**API endpoint:** `GET /tasks?handler=X&status=Y&since=Z&limit=50`

### Query 2 — Task Drill-Down

**Use case:** Full Task detail — `schema_output`, `actions`, `event_raw`, `error`, `otel_trace_id` deep link.

**Access pattern:** Single-row fetch by primary key.

**Indexes used:** Primary key (no additional index needed).

**API endpoint:** `GET /tasks/{id}`

### Query 3 — Retry Lineage Chain

**Use case:** Display the full retry chain from any Task in the chain — original execution plus all subsequent retries.

**Access pattern:** Recursive walk via `retry_of` FK. Walk upward to find the root (no `retry_of`), then fetch all Tasks sharing that root.

**Indexes used:** `idx_tasks_retry_of` for the FK join leg.

**API endpoint:** `GET /tasks/{id}/lineage`

### Query 4 — Processing Tasks (Timeout Management)

**Use case:** Show all in-flight Tasks ordered by age. Operator observes elapsed time and marks stuck Tasks as `Timeout`.

**Access pattern:** Filter by `status = 'Processing'`, ordered by `received_at ASC`. Frontend computes elapsed time from `received_at` — no epoch computation at the DB layer.

**Indexes used:** `idx_tasks_status`.

**API endpoint:** `GET /tasks?status=Processing&sort=received_at:asc`

### Query 5 — Decision Distribution

**Use case:** Breakdown of LLM decision values per handler over a configurable time window. Rendered as a summary on the handler view.

**Access pattern:** Aggregate by `handler` and `schema_output->>'decision'` over a time window, filtered to `status = 'Completed'`.

**Indexes used:** `idx_tasks_handler` for the time-window + handler filter; JSONB extraction without a GIN index is acceptable at KAPE's volume.

**API endpoint:** `GET /tasks/decisions?handler=X&since=Z`

### Query 6 — Bulk Timeout

**Use case:** Operator marks all `Processing` Tasks older than a threshold as `Timeout` in one operation.

**Access pattern:** Update `status → Timeout`, `completed_at → now()` where `status = 'Processing'` and `received_at < threshold`. Returns affected task IDs for confirmation display.

**Indexes used:** `idx_tasks_status`.

**API endpoint:** `PATCH /tasks/bulk/status` with `{ "status": "Timeout", "olderThanSeconds": 3600 }`

---

## 7. Observability Boundary

This section makes the boundary between PostgreSQL and the OTEL backend explicit.

| Concern                               | Owner                              | How dashboard accesses it     |
| ------------------------------------- | ---------------------------------- | ----------------------------- |
| Task lifecycle, status, timing        | PostgreSQL (`tasks`)               | `kape-task-service` REST API  |
| LLM decision output                   | PostgreSQL (`tasks.schema_output`) | `kape-task-service` REST API  |
| Action outcomes                       | PostgreSQL (`tasks.actions`)       | `kape-task-service` REST API  |
| Triggering event (raw)                | PostgreSQL (`tasks.event_raw`)     | `kape-task-service` REST API  |
| Every MCP tool call during ReAct loop | OTEL backend (SigNoz / Tempo)      | Deep link via `otel_trace_id` |
| LLM prompt and response text          | OTEL backend                       | Deep link via `otel_trace_id` |
| LLM token counts, iteration count     | OTEL backend                       | Deep link via `otel_trace_id` |
| Handler throughput, latency p99       | Prometheus                         | Metrics backend (Grafana)     |
| Handler failure rate                  | Prometheus                         | Metrics backend (Grafana)     |

**`task_id` in OTEL traces:** The handler runtime sets `kape.task_id` as a span attribute on the root trace at execution start. This enables the OTEL backend to surface the Task ID alongside trace data, and enables cross-referencing in either direction — from a Task record to its trace, or from a trace to its Task record.

```python
span.set_attribute("kape.task_id",    task_id)
span.set_attribute("kape.handler",    config.kape.handler_name)
span.set_attribute("kape.cluster",    config.kape.cluster_name)
span.set_attribute("kape.event_type", event.type)
span.set_attribute("kape.dry_run",    config.kape.dry_run)
```
