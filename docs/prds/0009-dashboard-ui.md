# KAPE Dashboard

## Status

**Draft**

## Problem Statement

KAPE operators currently have no graphical interface for managing Task lifecycle. All task monitoring, timeout marking, and retry operations require direct access to `kape-task-service` via `kubectl port-forward` or API calls, creating friction in incident response workflows. A dedicated operational UI is needed to surface task lifecycle data in real time and provide one-click timeout and retry actions.

## Goals

- Provide a live-updating task feed that displays all tasks across all handlers in reverse chronological order with real-time SSE updates
- Enable operators to inspect full task details including triggering event, LLM decision output, action results, and error information on a single page
- Allow operators to mark stuck `Processing` tasks as `Timeout` (single and bulk) and retry failed tasks with confirmation dialogs
- Surface handler-level aggregate data including task volume, failure counts, and decision distribution without requiring Grafana or Kubernetes API access
- Deploy as a zero-auth-code React Router v7 application that uses OAuth2 Proxy for authentication and proxies all data through `kape-task-service`

## Non-Goals

- Expose Kubernetes metrics, LLM telemetry (prompts, tool calls, token counts), or handler throughput/latency charts -- these remain owned by Prometheus and Grafana
- Implement role-based access control (viewer vs. operator roles) -- deferred to v2
- Provide an API for programmatic dashboard access -- the dashboard is a human-consumable UI only

## User Stories

- As an on-call operator, I want a live feed of all tasks so that I can monitor handler execution in real time without polling `kape-task-service` directly.
- As an incident responder, I want to inspect a task's full detail -- triggering event, LLM decision, action outcomes, and any errors -- from a single page so that I can quickly diagnose failures.
- As a platform engineer, I want to mark a stuck `Processing` task as `Timeout` or retry a failed task with a single click so that I can unblock the execution pipeline without writing API calls.
- As a platform lead, I want to see handler-level aggregates (task volume, failure count, decision distribution) so that I can identify problematic handlers at a glance.
- As an operator handling multiple stuck tasks, I want to bulk-select `Processing` tasks and mark them as `Timeout` together so that I can clear a backlog without repetitive single operations.

## Functional Requirements

- FR-1: The dashboard SHALL display a live-updating task feed at `/tasks` that shows all tasks in reverse chronological order with filter controls for handler, status, time range, and event type.
- FR-2: The dashboard SHALL open an SSE connection to `kape-task-service` and upsert incoming `task.created` and `task.updated` events into the feed state in real time.
- FR-3: The dashboard SHALL provide a pause/resume toggle on the live feed that buffers incoming SSE events when paused and flushes them on resume.
- FR-4: The dashboard SHALL display a task detail page at `/tasks/:id` with a 2x2 grid of four panels: triggering event (syntax-highlighted JSON), LLM decision output (handler-specific key-value fields), action results (one row per `ActionResult`), and error information (shown only when `error` is non-null).
- FR-5: The dashboard SHALL render a `[ Retry ]` button on tasks in `ActionError`, `Failed`, `SchemaValidationFailed`, and `Timeout` statuses, and a `[ Timeout ]` button on `Processing` tasks, each opening a confirmation modal before executing the operation.
- FR-6: The dashboard SHALL allow bulk selection of `Processing` tasks via checkboxes and submit a single `PATCH /tasks/bulk/status` request with a confirmation modal.
- FR-7: The dashboard SHALL display a retry lineage chain at `/tasks/:id/lineage` showing all attempts sharing a root task, with each attempt's status, duration, and a link to its detail page.
- FR-8: The dashboard SHALL display a handlers table at `/handlers` derived from task-record aggregates showing handler name, namespace, last task time, 24h task count, 24h failure count, and currently processing count (live-updated via SSE).
- FR-9: The dashboard SHALL display a handler detail page at `/handlers/:name` with a stacked bar chart of decision distribution for `Completed` tasks in the selected time window and a breakdown table of non-completed tasks.
- FR-10: The dashboard SHALL require zero authentication code in its own process -- all authentication SHALL be enforced by OAuth2 Proxy, which injects `X-Forwarded-User` and `X-Forwarded-Email` headers on forwarded requests.

## Technical Context

The dashboard is deployed inside `kape-system` on any Kubernetes cluster and consists of two Deployments in a single Helm chart: `oauth2-proxy` and `kape-dashboard`. The Ingress routes all external traffic to OAuth2 Proxy first; the dashboard is cluster-internal and only reachable through OAuth2 Proxy. `kape-task-service` is also cluster-internal and reachable only from the dashboard -- a NetworkPolicy restricts its ingress to pods with the `app: kape-dashboard` label (see ADR 0006: "task-service is the exclusive mediator to PostgreSQL").

The dashboard consumes `kape-task-service` exclusively for all data. It never connects to PostgreSQL directly and never calls the Kubernetes API. Read operations use React Router v7 loaders (server-side); write operations (retry, timeout) use React Router actions. An SSE resource route at `GET /sse/tasks` proxies `kape-task-service`'s `GET /tasks/stream` endpoint, which is backed by an in-process SSE hub (see ADR 0010: "Stream task events via an in-process SSE hub"). The dashboard does not implement its own SSE hub -- it relays the upstream stream.

Timeout is an operator-driven decision, not a background sweep (see ADR 0015: "Timeout is an operator-driven UI decision, not a background sweep"). The dashboard computes elapsed time client-side from `received_at` and presents the operator with selectable rows. Bulk status updates are best-effort: the dashboard sends pre-filtered valid task IDs and inspects the `affected_ids` response to learn what changed (see ADR 0016: "Bulk status updates are best-effort with no DB-level transition validation").

The technology stack is React Router v7 in framework mode (Node.js server), TypeScript with OpenAPI-generated types from `kape-task-service`, TailwindCSS for styling, and Radix UI for accessible primitives (modal, dropdown, tooltip). SSE is implemented via `remix-utils` helpers (`eventStream`, `useEventSource`). The deployment runs on plain Node.js with no platform-specific infrastructure -- Next.js was explicitly ruled out as Vercel-optimised and hostile to arbitrary-cluster deployment.

## Design Tenets

- The dashboard never accesses PostgreSQL directly; all reads and writes are mediated by `kape-task-service` over cluster DNS.
- Zero authentication code in the dashboard process; OAuth2 Proxy is the sole authentication boundary.
- No Kubernetes API calls from the dashboard; handler metadata is derived from task records, not CRD reads.

## Open Questions

- Should the SSE resource route perform session validation before proxying the upstream stream, and if so, what form should that validation take given that OAuth2 Proxy has already authenticated the request?
- What is the exact OpenAPI spec contract between `kape-task-service` and the dashboard for the `/tasks/decisions` aggregate endpoint that feeds the decision distribution chart?
- Should the dashboard serve its own health-check endpoint for liveness/readiness probes, or rely on the Node.js server's default behaviour?

## Future Considerations

- **RBAC (v2):** Two roles -- `viewer` (read-only) and `operator` (read + timeout + retry) -- mapped to two GitHub teams, with OAuth2 Proxy injecting a `X-Forwarded-Groups` header and the dashboard enforcing role checks in write-action route handlers and hiding write-action UI elements from `viewer` users.
- **Approval management (v2):** A dedicated view for `PendingApproval` tasks allowing operators to approve or reject, triggering Argo Workflows suspend node resolution.
- **Handler-scoped feed (v2):** A dedicated handler-scoped feed page with handler-specific context rendered inline alongside the task list, beyond the current pre-filtered `/tasks?handler=<name>` navigation.

## References

- `docs/specs/0009-dashboard-ui/README.md` -- original technical design document
