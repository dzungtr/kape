# KAPE

KAPE (Kubernetes Agentic Platform Execution) is a Kubernetes-native, event-driven platform for running LLM agents. Platform engineers declare **intent** — prompts, guardrails, conditions — as custom resources, and the operator materialises the infrastructure to run them.

## Language

### Platform

**KAPE**:
The platform itself. Kubernetes Agentic Platform Execution. API group `kape.io/v1alpha1`, deployed in the `kape-system` namespace.

**Intent**:
What a platform engineer declares — prompts, guardrails, output schemas, routing conditions. The opposite of infrastructure wiring, which the operator owns.
_Avoid_: config, spec (when you mean the declarative goal rather than the YAML field)

**Operator**:
The Go controller that watches Kape* custom resources and materialises everything a Handler pod needs before it starts — skill content, file mounts, the KapeProxy config, sidecars.
_Deprecated (v1 platform)_: Kape* materialisation is superseded by the successor platform (#150); the successor controller owns the CRUD job. Retained as a re-role candidate.

**Handler runtime**:
The Python + LangGraph process inside a Handler pod that consumes an event and runs the agent. A pure message processor: it does not read CRDs, manage infrastructure, or hold database credentials.
_Deprecated (v1 platform)_: superseded by the successor platform (#150), where the agent runs inside a session-owned sandbox. Retained as a re-role candidate.
_Avoid_: agent (the runtime runs an agent; it is not itself "the agent")

### Custom resources

**KapeHandler**:
One complete agent pipeline, declared as a single custom resource. Couples a trigger, a prompt, tools, and an output schema.
_Avoid_: pipeline, agent definition

**KapeTool**:
A tool capability registration. Types: `mcp`, `memory`, `event-publish`. The memory-isolation boundary is the KapeTool instance — Handlers sharing a KapeTool share a vector DB collection.

**KapeSchema**:
The structured-output contract for an LLM decision. Carries a `spec.version`; a breaking change takes a new name.

**KapeSkill**:
A reusable, named, parameterised reasoning procedure that multiple Handlers can reference — operational knowledge ("how a competent SRE investigates order events") factored out of individual Handler prompts.

**KapeProxy**:
The single MCP federation sidecar injected per Handler pod. Connects to all upstream MCP servers, filters by per-tool allowlists, namespaces tool names, and exposes one unified MCP endpoint to the Handler runtime. Replaces the older one-sidecar-per-KapeTool model.

**KapePolicy**:
(v2) Cross-Handler guardrails. Not yet implemented.

### Event flow

**Event**:
A CloudEvent delivered over the broker that triggers a Handler. Sourced from adapters (AlertManager, k8s-audit, …).

**Adapter**:
A Go service that translates an external signal (an AlertManager alert, a Kubernetes audit log entry) into a KAPE Event on the broker.

**Action**:
An effect a Handler emits after reasoning. `event-publish` tools live in `actions[]` only — the LLM fills `$prompt` content fields and the engineer controls routing via conditions.

**Task**:
The persisted record of one Handler invocation — its event, reasoning trace, decision, and actions. The audit unit surfaced in the dashboard.
_Deprecated (v1 platform)_: superseded by the successor's session event log (#150); the task-service is retired as the system of record. Retained as a re-role candidate.

### Successor platform (M2)

**Session**:
The durable lifecycle unit of the successor platform: owns the sandbox storage, spans agent replacements and revives, and carries the event history. The unit that survives a controller drop.
_Avoid_: agent (a session outlives its agents), task (old-platform term).

**Agent (successor sense)**:
The live sandbox+pi instance serving a session at a given moment. A revive replaces the agent, not the session.
_Avoid_: Handler runtime (old-platform term).

**Event log**:
The authoritative record of session events. The single execution truth from which session state derives; everything else mirrors it.
_Avoid_: task-service event log, broker (old-platform mechanisms).

**Daemon**:
The agent-transport relay inside a sandbox: owns the pi subprocess, spools its records on the sandbox volume, serves them over passive HTTP. Never dials the controller.
_Avoid_: sidecar, kapeproxy (old-platform concepts; the daemon is the data plane, not an MCP federation proxy).

**Spool**:
The daemon's unacked-record buffer on the sandbox volume: verbatim pi records plus daemon notices, monotonic spool ids durable across daemon restarts. The backing store of the pull ack cursor.

**Puller**:
The controller-side per-agent loop that long-polls the daemon's spool and appends into the event log. The controller stays seq authority — the daemon never assigns client-facing seqs.

**boot_id**:
A fresh random id per daemon start, reported on `/status`. The partition-vs-replacement distinguisher: same boot_id after a pull outage means partition; a new one means the sandbox (and daemon and pi with it) was replaced (ADR-0086: the daemon is the sandbox's main process, so daemon death collapses into sandbox death).

**Main process**:
The canonical command the OpenShell supervisor launches once per sandbox (the `CreateSandboxRequest.command` field) — for KAPE agents, the **Daemon**. Image entrypoints never run; daemon lifetime equals sandbox lifetime.
_Avoid_: entrypoint, init container, profile hook (fallback pattern only).

**Revive**:
The automatic replacement of a dead agent while its **Session** persists. Tier 1 is in-place (the daemon lazily respawns a crashed pi on the same session, no budget); Tier 2 replaces the sandbox on the same PVC with `pi --resume` from the session-pinned session tree, budgeted (`REVIVE_MAX`, `REVIVE_COOLDOWN`) and driven from the event log.
_Avoid_: restart, retry (infrastructure acts; revive is session semantics).

**Paused (session state)**:
The FSM state a running session occupies while a Tier 2 revive is in flight or the revive budget is exhausted (`running ⇄ paused`, ADR-0086). Commands are rejected fail-fast while paused; exit is re-arm, successful revive, or Delete.
_Avoid_: stopped, suspended (OpenShell suspend/resume is a different mechanism), failed (sessions have no failed state).

**Crash window**:
What a sandbox or pi death loses: one in-flight turn plus pi's in-memory queue of steers/follow-ups. Accepted-but-undelivered commands stay derivable from the event log; nothing is auto-replayed after a revive.

**Subagent (sandbox sense)**:
A child sandbox agent created through the front door with parent linkage; capped (`SPAWN_MAX_DEPTH`, `SPAWN_MAX_CHILDREN`) and cascade-terminated with its parent. In-sandbox parallelism is the agent's own business, not this.
_Avoid_: pi conversation fork (in-sandbox parallelism — not a platform concept).

**Assessment**:
The controller's SLM-judged view of a running turn: a verdict plus a short summary, written as a controller-owned `agent.assessed` event and projected (latest only) onto agent status. Advisory — never an FSM state or transition.
_Avoid_: monitor state, health status (deterministic liveness is a separate concept).

**Verdict**:
The assessment's classification: `healthy | looping | blocked | degraded | finished | failed` — single responsibility: one owner and one action class per verdict (healthy→wait; looping→host steers/aborts; blocked→re-provision+revive; degraded→halt dispatches+diagnose platform; finished→consume; failed→retry/re-dispatch). Cheap purpose-built endpoints produce it (summarizer + decision-modality classifier); deterministic events stay authoritative.
_Avoid_: agent status, session state (the FSM has no stuck/looping/blocked state).
_Disambiguate_: the verdict `degraded` (SLM, provider/infra read) vs the liveness layer's `degraded` daemon state (deterministic, ADR-0083) — same word, different layers.

**Tick**:
One periodic mid-turn assessment firing while a turn runs (60s default, configurable); armed and disarmed by registry lifecycle transitions, never persisted.

**Terminal assessment**:
The single settle-path assessment: synchronous but hard-bounded (10s budget); failure yields no terminal verdict and never blocks settle.

**Wait**:
The host-side blocking long-poll (`wait_agent`): one held connection returning a minimal coalesced snapshot on FSM state change, verdict transition, liveness failure, or timeout. The host's primary monitoring interface; raw records never cross it.
_Avoid_: polling `read_events` as monitoring (demoted to a tail-pull mechanism).

## Relationships

- A **KapeHandler** references zero or more **KapeTool** and **KapeSkill** resources; the **Operator** unions their tool refs into one **KapeProxy** config.
- A **KapeHandler** produces structured output conforming to a **KapeSchema**.
- An **Adapter** emits **Events**; a **KapeHandler** is triggered by **Events** and emits **Actions**.
- Handlers sharing a **KapeTool** of type `memory` share one vector DB collection.

## Example dialogue

> **Dev:** "When the payment-failure **Event** arrives, does the **Handler runtime** read the KapeSkill from the cluster?"
> **Platform engineer:** "No — the runtime never touches CRDs. The **Operator** materialises the **KapeSkill** content into the pod before it starts. The runtime just processes the message."

## Flagged ambiguities

- "agent" was used for both the running process and the declarative pipeline — resolved: the process is the **Handler runtime**, the declarative pipeline is a **KapeHandler**.
- "spec" was overloaded between the declarative *intent* and the YAML `spec:` field, and between design docs and CRD schemas — in this glossary, **Intent** is the concept; "spec" means the YAML field only.
