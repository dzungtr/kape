# Phase 10.3 — Examples

**Status:** pending
**Phase:** 10-helm-polish
**Milestone:** M6
**Specs:** 0011

## Goal

Ship complete, working example manifests for the two primary use cases: AlertManager handler and K8s Audit handler. Each example is self-contained and can be applied directly after Helm install.

## Work

### `examples/alertmanager-handler/`
- `kapehandler.yaml`: `KapeHandler` with LLM config, `spec.tools` referencing the memory tool, `spec.skills` referencing the triage skill
- `kapetool.yaml`: `KapeTool` of type `memory` named `alert-memory`
- `kapeschema.yaml`: `KapeSchema` with JSON schema for alert triage output (`severity`, `action`, `summary` fields)
- `kapeskill.yaml`: `KapeSkill` named `alert-triage` with `spec.instruction` for alert analysis

### `examples/audit-handler/`
- `kapehandler.yaml`: `KapeHandler` configured for K8s audit events
- `kapetool.yaml`: `KapeTool` of type `mcp` referencing a kubectl MCP tool endpoint
- `kapeschema.yaml`: `KapeSchema` for audit response (`risk_level`, `action`, `justification`)
- `kapeskill.yaml`: `KapeSkill` named `audit-review` for security audit analysis

## Acceptance Criteria

- `kubectl apply -f examples/alertmanager-handler/` → all resources created; KapeHandler reaches `Ready` status
- `kubectl apply -f examples/audit-handler/` → all resources created; KapeHandler reaches `Ready` status
- Firing a test alert → Task appears in dashboard with populated `schema_output`

## Key Files

- `examples/alertmanager-handler/kapehandler.yaml` (new)
- `examples/alertmanager-handler/kapetool.yaml` (new)
- `examples/alertmanager-handler/kapeschema.yaml` (new)
- `examples/alertmanager-handler/kapeskill.yaml` (new)
- `examples/audit-handler/kapehandler.yaml` (new)
- `examples/audit-handler/kapetool.yaml` (new)
- `examples/audit-handler/kapeschema.yaml` (new)
- `examples/audit-handler/kapeskill.yaml` (new)
