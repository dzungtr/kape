# Contributing to KAPE

Welcome! KAPE is an early-stage project; contributions in any form — bug reports, docs, features, and fixes — are appreciated. This guide covers everything you need to build, test, and submit changes.

---

## Prerequisites

| Tool | Version | Notes |
|---|---|---|
| Go | 1.25+ | Workspace modules under `go.work` |
| [golangci-lint](https://golangci-lint.run/) | latest | Go linting |
| [controller-gen](https://github.com/kubernetes-sigs/controller-tools) | latest | CRD/RBAC code generation |
| Python | 3.12+ | `runtime/` only; managed via [uv](https://docs.astral.sh/uv/) |
| [uv](https://docs.astral.sh/uv/) | latest | Python package manager (replaces pip/venv) |
| Node.js | 20+ | `dashboard/` only |
| podman | latest | Container builds — **do not use docker** |
| kubectl | latest | Operator integration testing |
| [kind](https://kind.sigs.k8s.io/) | latest | Recommended local cluster |
| Helm | 3+ | Installing the chart |

> CI is in progress. For now, run the checks locally before opening a PR.

---

## Repo layout

```
adapters/       CloudEvents adapters (Falco, Alertmanager, K8s Audit)
charts/kape/    Helm chart for cluster deployment
cmd/            Shared CLI entrypoints (per module)
config/         Operator configuration
crds/           Generated CRD manifests (do not edit by hand)
dashboard/      React frontend
docs/           Architecture, CRD reference, design specs
examples/       Reference KapeHandler/KapeTool/KapeSchema YAML
operator/       Kubernetes operator (controller-gen, controller-runtime)
runtime/        LangGraph ReAct agent runtime (Python)
task-service/   Task tracking microservice
```

---

## Build & test

All top-level commands are in the `Makefile`.

### Go modules (operator / task-service / adapters)

```bash
# Build all Go binaries
make build

# Run all Go tests
make test

# Lint (golangci-lint)
make lint

# Regenerate CRDs and RBAC from operator types
make generate
```

You can also target a single module directly:

```bash
go test ./operator/...
go test ./task-service/...
go test ./adapters/...
```

### Python runtime

```bash
# Run tests
cd runtime && uv run pytest

# Lint + format check
cd runtime && uv run ruff check . && uv run ruff format --check .

# Build wheel
cd runtime && uv build
```

### Dashboard

```bash
cd dashboard && npm install
npm run build
npm test -- --passWithNoTests
npm run lint
```

### Container images (podman)

The `Makefile` targets use `docker build` — substitute `podman build` locally:

```bash
podman build -t kape-operator:dev -f operator/Dockerfile .
podman build -t kape-runtime:dev  -f runtime/Dockerfile .
# etc.
```

---

## Branching & commit conventions

Branch from `main`. Name your branch `<type>/<short-description>`, e.g.:

```
feat/nats-backpressure
fix/crd-schema-validation
docs/contributing-guide
chore/upgrade-go-1.25
```

This project follows [Conventional Commits](https://www.conventionalcommits.org/):

| Prefix | When to use |
|---|---|
| `feat:` | New feature or capability |
| `fix:` | Bug fix |
| `chore:` | Tooling, deps, CI, non-functional |
| `doc:` / `docs:` | Documentation only |
| `refactor:` | Code restructure without behaviour change |
| `test:` | Test additions or corrections |

Commit message format:

```
<type>(<scope>): <short imperative summary>

Optional body explaining the why, not the what.
```

Keep the subject line under 72 characters. Reference issues as `Fixes #123` in the body when applicable.

---

## PR checklist

Before marking a PR ready for review:

- [ ] `make build` passes
- [ ] `make test` passes
- [ ] `make lint` passes with no new findings
- [ ] If you modified operator types, `make generate` was run and the updated CRDs are committed
- [ ] **Snyk scan**: run `snyk code` on changed Go or Python files; resolve any high/critical findings
- [ ] **SBOM comment**: post a Snyk CycloneDX 1.4 SBOM summary table as a PR comment for each affected Go module (`./adapters`, `./operator`, `./task-service`) — see [kape-io CLAUDE.md](CLAUDE.md) for the exact format

---

## Code style

**Go:** `gofmt` (enforced by golangci-lint). Follow standard Go idioms; avoid unnecessary abstractions.

**Python:** [`ruff`](https://docs.astral.sh/ruff/) for linting and formatting, configured in `runtime/pyproject.toml` (`line-length = 100`, `target-version = py312`).

**TypeScript/React:** ESLint as configured in `dashboard/`.

---

## Helm chart layout

The chart lives at:

```
charts/kape/Chart.yaml
charts/kape/templates/
charts/kape/values.yaml
charts/kape/package.json
```

This follows the standard Helm convention (`charts/<name>/`):

- It matches the directory structure produced by `helm pull` and expected by `helm package` / OCI push defaults.
- It mirrors how charts are organized in major repos (e.g. `prometheus-community/helm-charts`, `grafana/helm-charts`).
- The `charts/` prefix leaves natural room for additional charts (`charts/kape-adapters/`, `charts/kape-runtime/`) without restructuring the top-level repo.

> The chart previously lived at a flat `helm/` directory at the repo root; it was moved in the `docs/helm-charts-rename-wip` branch.

---

## Reporting issues & security disclosures

For bugs and feature requests, open a GitHub issue with a clear title and reproduction steps.

For security vulnerabilities, **do not open a public issue**. Instead, open a [private security advisory](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability) on GitHub, or email `security@kape.io` (placeholder — update with real contact before first public release).

---

## License

KAPE is licensed under the [Apache License 2.0](LICENSE).

By submitting a contribution you agree that your work will be made available under the same license.

```
Copyright 2026 KAPE Contributors
```
