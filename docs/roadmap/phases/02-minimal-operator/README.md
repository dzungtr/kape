# Phase 2 — Minimal Operator

**Status:** done
**Milestone:** —
**Specs:** 0002, 0005
**Modified by:** 0012 (created)

## Goal

The operator can watch a `KapeHandler` CRD and provision the handler Deployment with a correct `settings.toml` ConfigMap. This validates the operator→runtime configuration contract before the runtime is written.

## Reference Specs

- `0002-crds-design` — KapeHandler field reference
- `0005-kape-operator` — KapeHandler reconciler design, Deployment builder, ConfigMap rendering, status conditions

## Work

- Implement `KapeHandler` reconciler in `operator/controller/handler.go`
- On create/update: render `settings.toml` ConfigMap from KapeHandler spec
- On create/update: create/update handler Deployment (image, env vars, ConfigMap mount)
- On delete: delete Deployment and ConfigMap
- Status reconciliation: write `status.conditions` based on Deployment readiness
- Wire reconciler into `cmd/main.go` with `ff` config (kubeconfig, leader election, metrics addr)
- Run operator locally via `make run` against kind cluster

**Not in this phase:** KapeTool reconciler, sidecar injection, KEDA ScaledObject, webhook validation.

## Acceptance Criteria

- Apply a KapeHandler → Deployment appears with correct image, env vars, and mounted settings.toml
- Update the KapeHandler's `spec.llm.model` → Deployment rolls with updated ConfigMap
- Delete the KapeHandler → Deployment and ConfigMap are removed
- `kubectl get kapehandler <name> -o yaml` shows `status.conditions` with `Ready: False`

## Key Files

- `operator/controller/handler.go`
- `operator/reconcile/handler.go`
- `operator/cmd/main.go`
- `operator/infra/k8s/`
