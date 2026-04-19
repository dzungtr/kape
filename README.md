# KAPE — Kubernetes Agentic Platform Execution

> Autonomous cluster monitoring, reasoning, and remediation — declared entirely in Kubernetes CRDs.

![License](https://img.shields.io/badge/license-Apache%202.0-blue)
![Go](https://img.shields.io/badge/go-1.23-00ADD8)
![Python](https://img.shields.io/badge/python-3.12-3776AB)

## Why KAPE?

Kubernetes clusters generate constant signal — security violations, resource pressure, policy breaches, cost anomalies.
Existing tools either **act without reasoning** (autoscalers, Kyverno) or **reason without acting** (K8sGPT).
KAPE does both.

- **Event-driven** — ingests signals from Falco, Cilium, Kyverno, Karpenter, and K8s Audit via CloudEvents
- **LLM-powered reasoning** — LangGraph ReAct agents analyse context and produce structured decisions
- **Declarative config** — define agent behaviour entirely through `KapeHandler`, `KapeTool`, and `KapeSchema` CRDs — no code changes required
- **Scales to zero** — KEDA drives handler pods from NATS consumer lag; idle agents cost nothing

## Quick Start

```bash
helm install kape ./helm/kape --namespace kape-system --create-namespace
```

Then declare your first agent:

```yaml
apiVersion: kape.io/v1alpha1
kind: KapeHandler
metadata:
  name: falco-responder
spec:
  source: falco
  llm:
    model: gpt-4o
  tools:
    - name: kubectl-readonly
  schemaRef: incident-triage-v1
```

→ [Full getting-started guide](docs/)

## Documentation

| | |
|---|---|
| [Architecture](docs/architecture.md) | How the platform fits together |
| [CRD Reference](docs/crds.md) | KapeHandler, KapeTool, KapeSchema fields |
| [Design Specs](specs/) | RFCs and detailed technical decisions |
| [Contributing](CONTRIBUTING.md) | Development setup and workflow |

## Status

Early development — core operator, runtime, and adapters are functional. Dashboard and task-service in progress.
