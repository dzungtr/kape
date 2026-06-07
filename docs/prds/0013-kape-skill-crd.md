# Title: KapeSkill CRD, KapeProxy Federation, and KapeHandler Skill Integration

## Status
**Draft**

## Problem Statement

Platform engineers today duplicate investigation procedures across multiple KapeHandler system prompts -- each handler that responds to a different event (payment failure, fulfilment delay, shift anomaly) re-implements the same "check order events" logic, creating fragility, inconsistency, and maintenance overhead when the procedure changes. Simultaneously, the existing per-KapeTool sidecar model injects one `kapetool` container per referenced tool, but skills implicitly multiply tool references -- three skills each referencing two tools could mean six-plus sidecars per pod -- hitting Kubernetes container limits and making the architecture operationally unsound.

## Goals

- Introduce a KapeSkill CRD that lets platform engineers author reusable, parameterized investigation procedures once and reference them from multiple KapeHandler instances.
- Replace the N-to-N sidecar model with a single KapeProxy federation sidecar per handler pod that unions tool refs from both the handler and its referenced skills.
- Ensure the handler runtime never reads CRDs and that all skill content, file mounts, and proxy configuration are materialized by the operator before pod startup.
- Extend the operator's dependency gating, rollout hash computation, and label sync to cover skill references alongside existing schema and tool references.
- Preserve the existing handler runtime message-processing architecture with minimal changes (tool registry connection point, one built-in `load_skill` tool).

## Non-Goals

- Skill-to-skill nesting is not supported in v1 -- a KapeSkill cannot reference another KapeSkill.
- A separate `params[]` declaration block on skills is not provided -- all dynamic values come from the existing Jinja2 render context shared with the handler system prompt.
- No admission-time validation of tool name references inside skill instruction text; the engineer is responsible for using the correct namespaced `{kapetool-name}__{tool-name}` form.

## User Stories

- As a platform engineer, I want to define a reusable investigation procedure (e.g., "check order events") as a named KapeSkill so that multiple handlers can reference it without duplicating the instruction text across system prompts.
- As a platform engineer, I want to set `lazyLoad: true` on a skill so that its full instruction is only loaded by the agent on demand, optimizing context window usage for skills that are situationally relevant.
- As a platform engineer, I want skills to declare their own tool dependencies so that the operator automatically unions them with the handler's tools and provisions a single kapeproxy config instead of requiring me to redundantly declare tools on the handler.
- As a platform engineer, I want the operator to hold a handler in Pending state when a referenced KapeSkill does not exist or its tools are not Ready, so that pods are never deployed with incomplete or broken skill dependencies.
- As an agent runtime operator, I want to call `load_skill("check-order-events")` during the ReAct loop to retrieve the full resolution procedure text resolved against the live event context, so that I can decide which investigation techniques to apply at inference time.

## Functional Requirements

- FR-1: The system shall define a `KapeSkill` CRD (`kape.io/v1alpha1`) with required fields `spec.description` and `spec.instruction`, and optional fields `spec.lazyLoad` (boolean, default false) and `spec.tools[]` (list of KapeTool refs).
- FR-2: The KapeSkill `spec.instruction` shall support Jinja2 template syntax with the same render context as the KapeHandler system prompt: `event`, `cluster_name`, `handler_name`, `namespace`, `timestamp`, and `env`.
- FR-3: When `spec.lazyLoad` is `false` (eager), the operator shall render the skill instruction as a raw Jinja2 template string and append it directly into the handler's `system_prompt` in `settings.toml`, separated by `---` horizontal rules in declaration order.
- FR-4: When `spec.lazyLoad` is `true` (lazy), the operator shall write the raw instruction text to a file at `/etc/kape/skills/{skill-name}.txt` via a ConfigMap `kape-skills-{handler-name}` mounted into the `kapehandler` container, and shall inject a preamble block into the system prompt listing all lazy skills by name and description.
- FR-5: The operator shall inject exactly one `kapeproxy` sidecar container per handler pod (replacing the previous per-KapeTool sidecar model), which connects to all upstream MCP servers, filters by per-upstream `allowedTools`, namespaces tool names as `{kapetool-name}__{tool-name}`, and exposes a single unified MCP endpoint on port `:8080`.
- FR-6: The operator shall compute a unified tool map by deduplicating `KapeTool` references from both `KapeHandler.spec.tools[]` and the union of all referenced `KapeSkill.spec.tools[]`, keyed by KapeTool name, and render this into a single `kapeproxy-config-{handler-name}` ConfigMap.
- FR-7: The handler's dependency gate shall be extended so that a handler enters Pending state if any referenced KapeSkill does not exist (`KapeSkillNotFound`) or if its `status.conditions[Ready]` is not True (`KapeSkillNotReady`).
- FR-8: The rollout hash computation shall be extended to include all referenced `KapeSkill.spec` entries so that any change to a skill's instruction, description, lazyLoad, or tool refs triggers a Deployment rollout.
- FR-9: The handler runtime shall register a built-in `load_skill` LangGraph tool at startup that reads `/etc/kape/skills/{skill-name}.txt`, renders it with the live Jinja2 context, and returns the resolved instruction text, returning a not-found message gracefully when the skill file or directory does not exist.
- FR-10: The handler runtime shall connect to kapeproxy at a single endpoint (`config.proxy.endpoint`) instead of creating one MCPToolkit per kapetool sidecar, and shall read tool names in their already-namespaced `{kapetool-name}__{tool-name}` form.

## Technical Context

The KapeSkill and KapeProxy feature touches three main subsystems of the kape-io architecture: the operator (Go), the handler runtime (Python/LangGraph), and the sidecar (Go KapeProxy).

The **operator** (custom controller, Go) is the orchestrator of all Kape CRDs. It already manages KapeHandler and KapeTool reconciliation, computes rollout content hashes per ADR 0031, and enforces deletion protection via finalizers per ADR 0032. This feature adds a new `KapeSkillReconciler` with analogous duties (validation, tool readiness gating, finalizer for deletion protection) and extends the existing `KapeHandlerReconciler` with skill dependency gating, tool union computation (handler tools + all skill tools), system prompt assembly (handler prompt + eager skills + lazy skill preamble), lazy skill ConfigMap generation (`kape-skills-{handler-name}`), proxy config rendering (`kapeproxy-config-{handler-name}`), rollout hash extension, and label sync extension (`kape.io/skill-ref-{skillname}=true`).

The **handler runtime** (Python, LangGraph) is a pure message processor that never reads Kubernetes CRDs -- a principle codified in ADR 0002. All configuration, including skill content and tool routing, is materialized by the operator before the pod starts. The runtime changes are minimal: the tool registry connects to one kapeproxy endpoint rather than one per kapetool sidecar, `settings.toml` replaces per-tool `[tools.*]` MCP sections with a single `[proxy]` section (memory-type tools retain their own section since they connect directly to Qdrant), and a built-in `load_skill` tool is registered at startup for on-demand lazy skill loading.

The **KapeProxy** sidecar (Go) is a new component replacing the per-KapeTool sidecar model. It implements MCP federation: at startup it connects to all upstream MCP servers via SSE or streamable-http, fetches each upstream's tool catalog, applies per-upstream `allowedTools` filtering, namespaces remaining tools with the `{kapetool-name}__{tool-name}` pattern, and exposes a single MCP endpoint on `:8080`. Tool calls are routed by prefix, with input/output redaction per ADR 0004, OTEL trace propagation, and structured MCP errors for disallowed or unreachable tools.

## Design Tenets

- The handler runtime must never read Kubernetes CRDs or hold database credentials (ADR 0002). All skill content, proxy configuration, and file mounts must be materialized by the operator before the pod starts.
- The kapeproxy sidecar must be the only sidecar injected per handler pod regardless of how many KapeTools are referenced across the handler and its skills. No per-tool sidecar proliferation.
- Lazy skill file mounts and the `load_skill` tool must be functional but never required. A handler with zero skills or all eager skills must work identically to today.

## Open Questions

- What are the default resource requests and limits for the kapeproxy sidecar, and should they be configurable per handler?
- Should kapeproxy support hot-reload of its config file when the `kapeproxy-config-{handler-name}` ConfigMap changes, or is a pod rollout via the hash mechanism sufficient?
- Does the operator need to expose kapeproxy health in a handler status condition, or should unreachable upstreams only surface as MCP errors at tool-call time?

## Future Considerations

- Skill-to-skill nesting (a KapeSkill referencing another KapeSkill) could enable higher-level composition patterns but is deferred to a later iteration.
- Admission-time validation of tool name references inside skill instruction text could catch misconfiguration earlier but requires the operator to parse and lint Jinja2 templates, which is significant effort with unclear return.
- A dashboard or CLI command to list available skills, their descriptions, and which handlers reference them could improve discoverability for platform engineers.

## References

- Original spec: `docs/specs/0013-kape-skill-crd/README.md`
- ADR 0004: KapeProxy -- one MCP federation sidecar per pod instead of one sidecar per KapeTool
- ADR 0002: Handler runtime is a pure message processor and never reads Kubernetes CRDs
- ADR 0031: Trigger handler rollouts via a content hash over handler, schema, and tool specs
- ADR 0032: KapeSchema deletion protection finalizer
