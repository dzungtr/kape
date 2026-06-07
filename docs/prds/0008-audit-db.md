# Title: Audit Database for Task Execution Records

## Status

**Draft**

## Problem Statement

KAPE processes events through handlers that invoke LLM reasoning and execute actions, but currently has no persistent store for the resulting execution records. Without a durable audit trail, operators cannot review past decisions, investigate failures, walk retry lineage chains, or query the system to understand what happened and why.

## Goals

- Provide a durable, immutable audit trail for every Task execution, from initial event receipt through terminal outcome.
- Enable operators to query, filter, and drill into Task records via a dashboard, including retry lineage, decision distributions, and deep-links to OTEL traces.
- Support manual operator interventions such as marking stuck Tasks as timed out or triggering retries.
- Ensure all reads and writes to the audit store are mediated through a single service boundary (`kape-task-service`), not via direct database access.

## Non-Goals

- Replace Prometheus or the OTEL backend for metrics, alerting, or fine-grained tool-call tracing. The audit DB owns only Task lifecycle and decision records.
- Provide a real-time event stream or push-based notification channel to the dashboard. The dashboard queries the audit store via REST pull.
- Store LLM prompt/response text or individual MCP tool call details. These are owned by the OTEL backend to avoid duplicating PII exposure surface.

## User Stories

- As an operator, I want to view a live feed of recent Tasks filtered by handler and status, so that I can monitor what the system is processing right now.
- As an operator, I want to drill into a single Task to see its LLM decision output, actions that ran, error details, and the raw triggering event, so that I can investigate a failure or verify a result.
- As an operator, I want to walk the full retry lineage chain from any Task in the chain back to the original execution, so that I can understand the full history of a recurring failure.
- As an operator, I want to view all in-flight (Processing) Tasks ordered by age and manually mark stuck ones as timed out, so that I can unblock the pipeline without waiting for an automated sweep.
- As an operator, I want to see a breakdown of LLM decisions per handler over a configurable time window, so that I can assess what actions the system is taking and spot anomalies.

## Functional Requirements

- FR-1: The system must persist every Task created by a handler as a row in a `tasks` table, recording identity, event provenance, execution status, output, error details, lineage, and timing.
- FR-2: The `kape-task-service` REST API must be the sole component that reads and writes the `tasks` table; no handler runtime, operator, or dashboard component may connect to PostgreSQL directly.
- FR-3: The system must support querying Tasks by `received_at` time window, with optional filters for `handler` and `status`, paginated and ordered by `received_at DESC`.
- FR-4: The system must support single-Task drill-down by `task_id` (primary key), returning the full record including `schema_output`, `actions`, `event_raw`, `error`, and `otel_trace_id`.
- FR-5: The system must support walking the full retry lineage chain from any Task in the chain, returning the original execution plus all subsequent retries.
- FR-6: The system must support a bulk status update endpoint that accepts a list of Task IDs and a target status, returning only the IDs that were actually updated.
- FR-7: The `tasks` table must be range-partitioned by month on `received_at`, with partition creation handled by `kape-task-service` or a Kubernetes CronJob, and a default retention window of 90 days enforced by dropping old partitions.
- FR-8: Retry execution must re-publish the original `event_raw` to NATS with a `retry_of` extension pointing to the original Task ID, and the original Task must be marked `Retried` (terminal, never deleted).

## Technical Context

The audit database sits at the center of KAPE's event processing pipeline. Handlers produce Task records as they process CloudEvents; the dashboard consumes them for operator visibility. Both paths flow through `kape-task-service`, a Go REST API that is the exclusive mediator to PostgreSQL. No other component connects to the database directly, as established in ADR 0006 (task-service is the exclusive mediator to PostgreSQL).

The database is PostgreSQL managed by the CloudNativePG operator, chosen over ClickHouse because the access pattern mixes point lookups (retry routing, drill-down), filtered list queries (live feed), JSONB column access, and aggregates (decision distribution) -- a workload that PostgreSQL handles well. The `tasks` table is partitioned monthly by `received_at` (ADR 0012: Partition the tasks table monthly by received_at), enabling fast time-range scans via partition pruning and simple retention via `DROP TABLE`.

The Task lifecycle follows a state machine with nine statuses, six of which are terminal. Once a terminal status is recorded, the row is never updated -- this immutability is enforced in the application layer per ADR 0013 (Enforce terminal-state immutability in the application layer). Timeout is an operator-driven UI action, not an automated background sweep (ADR 0015: Timeout is an operator-driven UI decision, not a background sweep). Bulk status updates are best-effort: non-existent IDs are silently skipped and the response returns only the IDs actually updated (ADR 0016: Bulk status updates are best-effort with no DB-level transition validation).

Observability responsibility is divided along clear lines: the audit DB owns Task lifecycle, status, timing, LLM decision output, action outcomes, and the raw triggering event. The OTEL backend owns every MCP tool call during the ReAct loop, LLM prompt/response text, and token counts. Prometheus owns handler throughput, latency p99, and failure rate. A `kape.task_id` span attribute on the root OTEL trace enables deep-linking in either direction.

## Design Tenets

- All access to the audit store must go through `kape-task-service`. No direct database connections, no ad-hoc queries from handler pods, and no dashboard SQL clients.
- The audit DB is an audit store, not a metrics store. Handler health aggregates belong to Prometheus and the OTEL backend; the audit DB owns Task lifecycle and decision records only.
- Terminal states are immutable once written. A Task in `Completed`, `Failed`, `SchemaValidationFailed`, `ActionError`, `UnprocessableEvent`, `Timeout`, or `Retried` status must never transition again.

## Open Questions

- Should the monthly partition CronJob ship as part of the `kape-task-service` Helm chart, as a standalone CronJob in the `kape-system` namespace, or should partition creation be handled exclusively by `kape-task-service` on first write to a new partition?
- What is the expected write volume per handler per minute in production, and does the default 90-day retention window hold up under that load with the current index set?
- Should the `PendingApproval` enum value be included in v1 schema or deferred entirely until the v2 approval flow is designed, given it will never be written in v1?

## Future Considerations

- Approval workflows (the `PendingApproval` status is reserved in the v1 `task_status` enum but never written by the handler runtime in v1).
- GIN indexes on `schema_output` for ad-hoc JSONB queries if decision distribution queries become a performance bottleneck at higher volumes.
- Push-based notification from `kape-task-service` to the dashboard for real-time UI updates on Task status changes, rather than polling via REST.

## References

- [Spec: KAPE Audit Database -- Technical Design](../specs/0008-audit-db/README.md)
- ADR 0006: task-service is the exclusive mediator to PostgreSQL
- ADR 0012: Partition the tasks table monthly by received_at
- ADR 0013: Enforce terminal-state immutability in the application layer
- ADR 0015: Timeout is an operator-driven UI decision, not a background sweep
- ADR 0016: Bulk status updates are best-effort with no DB-level transition validation
