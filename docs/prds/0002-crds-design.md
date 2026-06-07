# CRD-Based Agent Configuration (KapeHandler, KapeTool, KapeSchema)

## Status
**Draft**

## Problem Statement

Platform engineers currently have no declarative, Kubernetes-native way to define autonomous AI agent behaviour for cluster operations. Today, every agent pipeline requires custom code, bespoke wiring between LLM providers, tool registries, event brokers, and action routers — making agent creation expensive, fragile, and non-portable across clusters. Engineers need an intent-based CRD API that allows them to describe triggers, reasoning, tools, and actions as YAML, and have the platform materialize the full runtime automatically.

## Goals

- Reduce the time to author and deploy a new cluster agent from days to minutes by providing declarative CRDs (KapeHandler, KapeTool, KapeSchema).
- Enable platform engineers to define agent behaviour (trigger, LLM config, tool references, structured output contract, and deterministic actions) entirely in YAML without writing application code.
- Ensure the platform operator handles all infrastructure provisioning: sidecar injection, vector database setup, KEDA ScaledObject generation, and ConfigMap materialization.
- Provide a structured output contract (KapeSchema) that enforces LLM decision format and prevents schema-drift from breaking downstream consumers.
- Support MCP tool security through a three-layer model: upstream RBAC, sidecar-level allowlists, and sidecar-level input/output redaction.

## Non-Goals

- This document does not cover the KapePolicy CRD, which is deferred to v2 for cross-handler guardrails and namespace-level constraints.
- This document does not define the dashboard or operator UI — the CRDs are consumable via kubectl and GitOps without a web UI.
- This document does not specify the internal Kape Handler Runtime LangGraph implementation beyond the execution model necessary to understand CRD semantics.

## User Stories

- As a platform engineer, I want to define a KapeHandler YAML with a NATS trigger, an LLM provider, tool references, and post-decision actions, so that a new cluster event-driven agent is deployed without writing or maintaining application code.
- As a platform security engineer, I want to control which MCP tools each handler can access via allowedTools in KapeTool, so that read-only investigation handlers cannot accidentally mutate cluster resources.
- As a platform engineer, I want to define a KapeSchema with a JSON Schema that the LLM's output must conform to, so that downstream automation and dashboards can consume agent decisions with a predictable, validated shape.
- As a site reliability engineer, I want to set trigger filters, dedup windows, and staleness timeouts per handler, so that noisy alert sources do not trigger redundant or stale agent invocation.
- As an operator, I want to set a dryRun flag on a KapeHandler, so that I can validate prompts, schemas, and action conditions against real events without executing side effects.

## Functional Requirements

- **FR-1** The system MUST provide a KapeHandler CRD that allows engineers to define, in a single resource, a trigger (NATS JetStream subscription with JSONPath filter, dedup, and staleness controls), an LLM provider configuration (provider, model, systemPrompt with Jinja2 templating, maxIterations), tool references, a schema reference, and an actions array with conditional execution.
- **FR-2** The system MUST provide a KapeTool CRD supporting three types: mcp (proxy to an external MCP server with allowedTools allowlist and input/output redaction), memory (vector database backend with configurable distance metric), and event-publish (named event publication endpoint for handler-to-handler chaining).
- **FR-3** The system MUST provide a KapeSchema CRD that accepts a JSON Schema defining the structured output contract, from which the handler runtime generates a Pydantic model for structured output enforcement via LangChain.
- **FR-4** The system MUST materialize each KapeHandler as a Kubernetes Deployment with the operator injecting environment variables (via secretKeyRef/configMapKeyRef), KEDA ScaledObject from spec.scaling, and one kapetool sidecar per referenced mcp-type KapeTool.
- **FR-5** The system MUST enforce that mcp-type tool requests pass through the kapetool sidecar's allowedTools allowlist (exact string and glob matching) and input/output redaction rules before reaching the upstream MCP server.
- **FR-6** The system MUST evaluate action conditions using safe Python expressions (simpleeval) against a sandboxed context containing the validated decision, the CloudEvents envelope, and environment variables, executing all eligible actions in parallel.
- **FR-7** The system MUST support Jinja2 templating in systemPrompt and action data blocks, with context variables including handler_name, cluster_name, namespace, timestamp, the full CloudEvents envelope, and all injected environment variables.
- **FR-8** The system MUST support a dryRun mode that executes the full agent loop (LLM calls, tool calls, schema validation) but skips all action execution, writing a Task record with status Completed and dry_run: true.
- **FR-9** The system MUST NOT provide automatic retry on failure — all retry is operator-initiated via the dashboard, with the kape-task-service re-publishing the original CloudEvent with a retry_of attribute.

## Technical Context

The CRD system is the northbound API for the entire KAPE platform. KapeHandler, KapeTool, and KapeSchema are the three resource types that platform engineers interact with; the KAPE operator is the sole runtime consumer, materializing these CRDs into Deployments, ConfigMaps, KEDA ScaledObjects, and sidecar containers. The handler runtime itself is a Python process built on LangGraph that executes a single ReAct loop with structured output enforcement — it never reads CRDs directly, only the ConfigMap and env vars the operator produces.

The system touches NATS JetStream (the event broker where handlers subscribe and emit CloudEvents), the kapetool sidecar (an MCP proxy injected per referenced mcp-type tool that enforces allowlists and redaction), kape-task-service (a separate service for Task persistence via POST/PATCH), and vector database backends (Qdrant, pgvector, or Weaviate for memory-type tools). KEDA handles auto-scaling per handler based on NATS consumer lag.

Relevant ADRs:
- ADR-0002 (handler-runtime-never-reads-crds): the operator, not the handler pod, reads CRDs and materializes them into ConfigMaps and env vars.
- ADR-0003 (no-context-section-agent-self-enriches): the declarative context section was removed; the LLM fetches data during its ReAct loop via MCP tools.
- ADR-0005 (event-publish-tools-in-actions-only): event-publish tools are in actions[], not tools[], so the LLM cannot decide to publish events.
- ADR-0004 (kapeproxy-single-federation-sidecar): relates to the sidecar architecture pattern used by kapetool.
- ADR-0031 (handler-rollout-content-hash): addresses handler Deployment update strategy tied to CRD spec content.
- ADR-0032 (kapeschema-deletion-protection-finalizer): protects KapeSchema resources from accidental deletion while referenced by active handlers.

## Design Tenets

- **Intent over implementation** — engineers declare what they want (trigger, tools, decision shape, actions) in YAML, and the platform handles all runtime wiring: sidecar injection, vector DB provisioning, KEDA scaling, ConfigMap materialization.
- **Platform owns infrastructure** — the operator provisions MCP sidecars, vector database backends, ScaledObjects, and env var injection. The handler pod has no Kubernetes API access and consumes only a ConfigMap and env vars.
- **Explicit over implicit** — every capability, guardrail, and action is declared in the CRD. The LLM produces structured decisions; the engineer controls what happens next via action conditions. No automatic retry, no implicit tool access, no silent event publication.

## Open Questions

- Should KapeHandler support referencing tools from other namespaces, or is per-namespace tool isolation sufficient in v1?
- What is the exact mechanism for the operator to discover and provision the correct vector database backend when a memory-type KapeTool is applied — is an existing cluster provisioner expected, or does the operator deploy one?
- How does the operator handle the deletion of a KapeTool that is still referenced by active KapeHandlers — should this be blocked with a finalizer?
- Should maxIterations defaults be configurable globally via kape-config, and is per-handler override sufficient, or do we also need per-handler minimum and maximum enforcement?

## Future Considerations

- KapePolicy CRD (v2): cross-handler guardrails, namespace-level constraints, and policies that apply to multiple handlers without repeating configuration in each KapeHandler resource.
- Performance and scaling benchmarks: understanding the relationship between NATS consumer lag thresholds, LLM call latency (P99), and maxReplicas — currently left as engineer-configured knobs without platform guidance.
- Event schema registry: a centralized store for CloudEvents schemas that allows consumers to validate and discover event types emitted by handlers, enabling safer handler-to-handler chaining across teams.

## References

- Original spec: `docs/specs/0002-crds-design/README.md`
