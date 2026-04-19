# Phase 10.4 — Docs + Release

**Status:** pending
**Phase:** 10-helm-polish
**Milestone:** M6
**Specs:** 0011

## Goal

Write the self-contained demo runbook. Generate `CHANGELOG.md` per component via changesets. Tag `0.1.0` as the v1 Release Candidate.

## Work

- `DEMO.md` at repo root:
  1. Prerequisites (kind, helm, kubectl, nats-cli)
  2. `helm install kape ./helm --set llm.apiKeySecret=<your-key> -n kape-system --create-namespace`
  3. `kubectl apply -f examples/alertmanager-handler/`
  4. Fire test AlertManager payload via `curl`
  5. Watch Task appear in dashboard
  6. `helm uninstall kape` cleanup
- Run `npx changeset` for each component; commit `CHANGELOG.md` files:
  - `adapters/CHANGELOG.md`
  - `operator/CHANGELOG.md`
  - `task-service/CHANGELOG.md`
  - `runtime/CHANGELOG.md`
- Tag repo: `git tag v0.1.0`

## Acceptance Criteria

- `DEMO.md` runbook is self-contained: a new engineer can follow it on a fresh machine with only the prerequisites installed
- `helm uninstall kape` leaves no orphaned PVCs, CRDs, or namespaces
- `v0.1.0` tag exists on main

## Key Files

- `DEMO.md` (new)
- `adapters/CHANGELOG.md` (new)
- `operator/CHANGELOG.md` (new)
- `task-service/CHANGELOG.md` (new)
- `runtime/CHANGELOG.md` (new)
