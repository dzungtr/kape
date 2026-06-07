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

**Handler runtime**:
The Python + LangGraph process inside a Handler pod that consumes an event and runs the agent. A pure message processor: it does not read CRDs, manage infrastructure, or hold database credentials.
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
