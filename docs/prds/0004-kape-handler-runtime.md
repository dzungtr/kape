# Title: KAPE Handler Runtime

## Status
**Draft**

## Problem Statement
Each KAPE handler pod needs a runtime that can consume events from NATS JetStream, run an LLM-driven agent to reason about the event and produce structured output, execute deterministic post-decision actions, and persist audit records -- all within a single-concurrency message-processing loop. Without a designed runtime, each handler would need to reimplement the same agent loop, tool integration, telemetry, and error-handling patterns from scratch.

## Goals

- Define a single-concurrency message processor that reads events from NATS JetStream, runs a LangGraph ReAct agent, executes actions via a deterministic ActionsRouter, and persists Task records through kape-task-service
- Provide a built-in `load_skill` tool that loads lazy skill files from a mounted volume and renders them with live event context at call time
- Integrate OpenInference-based OTEL tracing for LangGraph nodes, LLM calls, and tool invocations without requiring LangSmith or a backend-specific dependency
- Support a dry-run mode that executes the full agent loop and action evaluation but skips action execution, enabling engineers to validate prompts and schemas against real events without side effects
- Define a retry flow that preserves lineage between original and retry Task records, routing ActionError retries to skip the LLM and re-run only failed actions

## Non-Goals

- Handling horizontal scaling or consumer concurrency tuning -- KEDA on NATS consumer lag is the scaling mechanism, not the runtime itself
- Managing Kubernetes CRDs, infrastructure, or database credentials -- the operator owns all infrastructure concerns; the handler is purely a message processor
- Defining UI surfaces for timeout detection or retry initiation -- these are dashboard concerns, not part of the handler runtime

## User Stories

- As a platform engineer, I want to deploy a KAPE handler that reads events from a NATS subject, runs an LLM agent, and executes post-decision actions so that I can automate incident investigation workflows without writing the agent loop from scratch.
- As an SRE authoring a skill, I want lazy-loaded skills that are read from a mounted volume and rendered against the live event context so that I can update skill content without redeploying the handler pod.
- As an operator, I want to enable dry-run mode on a handler so that I can validate LLM prompts and schema outputs against production events without producing side effects.
- As an incident responder, I want full traceability from a Task record to the corresponding OTEL trace so that I can inspect every LLM call, tool invocation, and action execution for a given event.
- As an engineer debugging a failed handler run, I want retry tasks to preserve lineage back to the original Task and, for ActionError retries, skip the LLM and re-run only the failed actions so that retries are fast and auditable.

## Functional Requirements

- FR-1: The handler runtime MUST load its configuration from a mounted ConfigMap fully materialized by the operator, using a dynaconf priority chain of CLI flags, environment variables, and the ConfigMap volume file.
- FR-2: The handler runtime MUST connect to a single kapeproxy sidecar over localhost via a single MCPToolkit connection for all federated MCP tools.
- FR-3: The handler runtime MUST support exactly one NATS JetStream pull consumer per pod with immediate ACK and single concurrency.
- FR-4: The handler runtime MUST run a LangGraph ReAct agent graph with nodes for entry routing, reasoning, tool calling, output parsing, schema validation, guardrail execution, and action routing.
- FR-5: The handler runtime MUST register a built-in `load_skill` tool at startup that reads skill files from `/etc/kape/skills/` and renders Jinja2 templates against the live event context.
- FR-6: The handler runtime MUST use `model.with_structured_output()` with a Pydantic SchemaOutput model generated at startup from the handler's schema configuration, and MUST fail fast (no automatic retry) on validation failure, writing a SchemaValidationFailed Task record.
- FR-7: The ActionsRouter MUST evaluate action conditions deterministically using `simpleeval` (never raw `eval`) and MUST execute all eligible actions in parallel via `asyncio.gather`, where failure of one action does not block others.
- FR-8: The handler runtime MUST support three action types: `event-emitter` (publish a CloudEvent to NATS), `save-memory` (write to Qdrant), and `webhook` (call an external HTTP endpoint).
- FR-9: In dry-run mode, the handler runtime MUST execute the full agent loop including LLM calls, tool calls, schema validation, and guardrails, but MUST skip all action execution and write a Task record with `dry_run: true` and full `action_results[]`.
- FR-10: The handler runtime MUST persist all Task records through HTTP calls to `kape-task-service` (two writes per event: POST on ACK receipt, PATCH on agent completion), and MUST never hold database credentials or connect to PostgreSQL directly.
- FR-11: On retry, the handler runtime MUST fetch the original Task from `kape-task-service`, create a new Task with `retry_of` linking to the original, and route retries based on the original task's status: ActionError retries skip the LLM and re-run only failed actions; all other retryable statuses run the full LLM path.
- FR-12: The handler runtime MUST instrument all LangGraph nodes, LLM calls, and tool invocations using `openinference-instrumentation-langchain` with OTLP HTTP export, and MUST inject W3C TraceContext headers into all kapeproxy tool calls.
- FR-13: The handler runtime MUST implement two guardrail layers: a LangChain PIIMiddleware layer (email/API key/credit card detection) and custom engineer-configurable deterministic hooks materialized from the KapeHandler CRD spec.

## Technical Context

The KAPE Handler Runtime is the execution core of the KAPE platform. Each KapeHandler CRD produces a Deployment whose pods run two containers: the Python handler runtime and the Go kapeproxy federation sidecar. The operator fully materializes all configuration -- settings, schema, system prompt, skill files, kapeproxy config -- into ConfigMaps and volumes before pods start. The runtime never reads CRDs or manages infrastructure; it is a pure message processor.

The runtime interfaces with several internal systems: NATS JetStream for event consumption, kapeproxy (federation sidecar, ADR 0004) for all MCP tool access, kape-task-service (Go REST API, ADR 0006) for all Task record persistence, a Qdrant vector store for memory-type tools, and an OpenTelemetry Collector for traces. Key architectural decisions already recorded include that the handler runtime never reads CRDs (ADR 0002), that task service is the sole PostgreSQL mediator (ADR 0006), that Task records use ULID IDs with keyset pagination (ADR 0011), that the tasks table is partitioned monthly (ADR 0012), and that timeout is operator-driven and a UI concern (ADR 0015). The handler runtime itself owns no data stores -- all persistence is delegated to kape-task-service and kapeproxy's span emission.

The retry flow is notable: the runtime does not manage retry state internally. Retries are initiated by the operator via the dashboard, which calls kape-task-service, which re-publishes the original event to NATS. The handler receives it as a fresh event with a `retry_of` extension. The entry_router node uses this extension to fetch the original Task and decide whether to run the full LLM path or (for ActionError) skip directly to re-running only failed actions. This keeps the handler stateless across event boundaries.

## Design Tenets

- The handler must never hold database credentials or connect to PostgreSQL directly -- all Task persistence goes through `kape-task-service` via HTTP.
- The ActionsRouter must be fully deterministic with no LLM involvement -- conditions evaluated with `simpleeval`, actions dispatched by type registry, all eligible actions running in parallel.
- Observability must be backend-agnostic -- `openinference-instrumentation-langchain` emits OpenInference semantic conventions over OTLP, decoupling from any specific backend vendor.

## Open Questions

- What is the exact mechanism for detecting a crashed handler pod mid-Task (status stuck at Processing) and should there be any automated staleness detection beyond the operator-driven manual timeout?
- Should the `load_skill` tool have caching (e.g., per-event or per-skill) to avoid re-reading from disk on repeated calls to the same skill within a single event processing turn?
- Is there a need for configurable retry limits for the LangGraph agent graph (e.g., max consecutive parse errors before giving up) beyond the existing `max_iterations` setting?

## Future Considerations

- PendingApproval status and the approval event flow are identified in the Task status enum but explicitly deferred to v2 -- no approval workflow is implemented in this version.
- Custom guardrail hooks beyond the built-in PIIMiddleware are identified as engineer-configurable but the specific hook API and registration mechanism are not detailed in the spec.
- A `tool_audit_log` table in PostgreSQL was considered but rejected in favor of OTEL-based tool call audit -- this decision should be revisited if compliance requirements demand database-level tool call records separate from traces.

## References

- Original spec: `docs/specs/0004-kape-handler/README.md`
