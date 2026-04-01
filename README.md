# KAPE — Kubernetes Agentic Platform Execution

KAPE is a Kubernetes-native, event-driven AI agent platform that enables autonomous cluster monitoring, decision-making, and remediation.

Engineers declare agent behaviour entirely through Kubernetes Custom Resources — no code changes required to extend the system.

## Overview

Modern Kubernetes clusters generate enormous volumes of signals: security violations, resource pressure, policy breaches, cost anomalies, and runtime anomalies. Existing tooling either acts without reasoning (autoscalers, Kyverno) or reasons without acting (K8sGPT). KAPE bridges the gap by combining:

- **Event-driven signal collection** from Cilium, Kyverno, Falco, Karpenter, K8s Audit
- **LLM-powered contextual reasoning** via LangGraph ReAct agents
- **Declarative CRD-based configuration** under `kape.io/v1alpha1`
- **Extensible tool execution** via MCP servers consumed through `KapeTool` CRDs

## How It Works

1. **Event producers** (Falco, Kyverno, Cilium, Karpenter, K8s Audit) emit CloudEvents to NATS JetStream via CloudEvents adapters.
2. The **Kape Operator** watches `KapeHandler` CRDs and provisions handler Deployments + KEDA ScaledObjects.
3. Each **handler pod** runs a LangGraph ReAct agent that:
   - Consumes the event from NATS
   - Self-enriches context by calling MCP tools during its reasoning loop
   - Produces a structured decision conforming to a declared `KapeSchema`
   - Executes deterministic post-decision actions (emit downstream events, persist audit record)
4. Handler pods **scale independently** via KEDA on NATS consumer lag.
5. All decisions are persisted as **Task records** via `kape-task-service`, powering the management dashboard.

## CRDs

| CRD | Responsibility |
|-----|---------------|
| `KapeHandler` | One complete agent pipeline — LLM config, tools, schema ref, scaling, retry policy |
| `KapeTool` | Tool capability registration (`mcp`, `memory`, `event-publish`) |
| `KapeSchema` | Structured output contract for LLM decisions |
| `KapePolicy` | (v2) Cross-handler guardrails |

## Implementation Stack

| Layer | Technology |
|-------|-----------|
| API group | `kape.io/v1alpha1` |
| Namespace | `kape-system` |
| Handler runtime | Python + LangGraph |
| LLM SDK | LangChain (`bind_tools`, `with_structured_output`) |
| MCP adapter | `langchain-mcp-adapters` (MCPToolkit) |
| Memory backend | Qdrant (primary) |
| Event broker | NATS JetStream |
| Scaling | KEDA (NatsJetStream scaler) |
| Observability | OTEL → Arize OpenInference, Prometheus |
| Operator | Go + controller-runtime |

## Design Specs

| Document | Description |
|----------|-------------|
| [`specs/0001-rfc`](specs/0001-rfc/README.md) | Master RFC — full platform design |
| [`specs/0002-crds-design`](specs/0002-crds-design/README.md) | CRD schema reference |
| [`specs/0003-q&a`](specs/0003-q&a/README.md) | Open questions resolution |
| [`specs/0004-kape-handler`](specs/0004-kape-handler/README.md) | Handler pod technical design |
| [`specs/0005-kape-operator`](specs/0005-kape-operator/README.md) | Kape Operator technical design |
| [`specs/0006-events-broker-design`](specs/0006-events-broker-design/README.md) | Event broker and adapter design |
| [`specs/0007-security-layer`](specs/0007-security-layer/README.md) | Security hardening specifications |
| [`specs/plan.md`](specs/plan.md) | Session-by-session discussion plan |

## Security Model

KAPE enforces security at every layer:
- MCP RBAC templates per tool type
- `KapeTool` allow/deny filtering — LLM never sees filtered tools
- Prompt injection defence via system prompt templates and input escaping
- Cilium NetworkPolicy restricting handler pod egress
- CEL validation on all CRD fields
- ESO-managed secrets for LLM API keys and MCP credentials
- Append-only audit log

## Status

This repository contains design specifications. Implementation is in planning (see [`specs/plan.md`](specs/plan.md) for the session index and progress).
