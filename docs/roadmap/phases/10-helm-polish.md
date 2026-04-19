# Phase 10 — Helm + Examples + Polish

**Status:** pending
**Milestone:** M6
**Specs:** 0011
**Modified by:** 0012 (created)

## Goal

Package the full stack as a Helm chart installable on a fresh cluster. Ship example manifests. Write the demo runbook. v1 Release Candidate.

## Reference Specs

- `0011-repo-structure` — Helm chart layout, image naming conventions, changeset-based release strategy

## Work

### Helm chart (`helm/`)
Templates for: NATS JetStream StatefulSet, CloudNativePG cluster, KAPE operator, task-service, AlertManager adapter, K8s Audit adapter, Dashboard, OAuth2 Proxy, CRDs, cert-manager Issuer + Certificate.

`helm/values.yaml`: all configurable defaults (image tags, resource requests, LLM provider, NATS retention, replicas)

### Examples
- `examples/alertmanager-handler/`: complete KapeHandler + KapeTool + KapeSchema + KapeSkill for AlertManager use case
- `examples/audit-handler/`: complete example for K8s Audit use case

### Documentation
- `DEMO.md`: step-by-step runbook — install Helm chart, apply examples, fire test alert, watch task in dashboard

### Release
- Image tagging: all components tagged `0.1.0` in `values.yaml`
- `npx changeset` → `CHANGELOG.md` per component → `0.1.0` tag

## Acceptance Criteria

- `helm install kape ./helm --set llm.apiKeySecret=my-secret -n kape-system` on fresh kind cluster: all pods Running within 5 minutes
- Apply `examples/alertmanager-handler/` → handler pod starts
- `curl` test AlertManager payload → Task appears in dashboard within 30 seconds
- `helm uninstall kape` cleanly removes all resources (no orphaned PVCs or CRDs)
- `DEMO.md` runbook is self-contained

**M6 gate:** Full install → real event → visible decision. v1 Release Candidate tagged.

## Key Files

- `helm/Chart.yaml`, `helm/values.yaml`, `helm/templates/`
- `examples/alertmanager-handler/`
- `examples/audit-handler/`
- `DEMO.md`
