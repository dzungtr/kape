# kape-io Project Instructions

## What this is

KAPE (Kubernetes Agentic Platform Execution) is a Kubernetes-native, event-driven platform for running LLM agents. Platform engineers declare intent — prompts, guardrails, conditions — as `Kape*` custom resources; the operator materialises the infrastructure, and the Python/LangGraph runtime processes events inside Handler pods.

Glossary: `CONTEXT.md`. Architecture decisions: `docs/adr/`.

## Repo structure

| Path | What it is | Toolchain |
|---|---|---|
| `operator/` | Kubernetes controller — watches Kape* CRDs, materialises Handler pods | Go (go.work) |
| `task-service/` | Task tracking microservice | Go (go.work) |
| `adapters/` | CloudEvents adapters (Falco, AlertManager, k8s Audit) | Go (go.work) |
| `kapeproxy/` | MCP federation sidecar per Handler pod | Go (go.work) |
| `runtime/` | LangGraph ReAct agent runtime | Python / uv / conda |
| `dashboard/` | React frontend for task feed | TypeScript / bun |
| `charts/` | Helm chart for cluster deployment | Helm |
| `crds/` | Generated CRD manifests — **do not edit by hand** | controller-gen |
| `config/` | Operator configuration | — |
| `playground/` | Local dev stack (podman compose) | — |
| `docs/` | Architecture docs, ADRs, CRD reference | — |
| `examples/` | Reference KapeHandler/KapeTool/KapeSchema YAML | — |

Deeper layout and prerequisites: `CONTRIBUTING.md`.

## Build, test, run

Entry points via `Makefile` from repo root:

```
make build      # all Go binaries, Python wheel, dashboard
make test       # all tests (Go, Python, dashboard)
make lint       # golangci-lint + ruff + ESLint
make generate   # regenerate CRDs and TypeScript API types
```

Local stack: `make playground-up` (podman compose, copies example configs on first run).

**Gotchas — wrong tool = broken environment:**

- **Python tests:** `conda run -n kape-runtime pytest` (not plain pytest)
- **Containers:** `podman` / `podman compose` — never `docker` / `docker compose`
- **Dashboard:** `bun` — never `npm` or `yarn`
- **Go tests:** require podman socket env (`DOCKER_HOST=unix://$(XDG_RUNTIME_DIR)/podman/podman.sock TESTCONTAINERS_RYUK_DISABLED=true`); `make test` sets this automatically

Full prerequisites and per-module commands: `CONTRIBUTING.md`. `Makefile` is the source of truth for all build targets.

## Backlog & issues

Issues and PRDs live as GitHub issues on `dzungtr/kape` — use `gh` CLI for all issue operations.

Full operational contract (label state machine, mutability table): `docs/agent-rituals.md`.  
Pocock vocabulary bridge: `docs/agents/triage-labels.md`.  
Taxonomy decision: `docs/adr/0001-github-label-taxonomy.md`.

Rituals as skills: `/standup`, `/triage`, `/to-issues`, `/to-prd`, `/apply-labels`.

## Domain docs

Before starting any task, run memsearch to orient:

```
memsearch search "<terms>" -c kape_io --top-k 5
```

or `/memsearch-search`. Indexed sources: `docs/`, `CONTEXT.md`, Go modules (see `.memsearch.toml`). After editing docs, re-index with `/memsearch-index`.

For deeper context on specific areas: `CONTEXT.md` (glossary) and `docs/adr/` — see `docs/agents/domain.md`.
