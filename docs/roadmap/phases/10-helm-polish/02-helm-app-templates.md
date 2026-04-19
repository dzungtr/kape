# Phase 10.2 — Helm Application Templates

**Status:** pending
**Phase:** 10-helm-polish
**Milestone:** M6
**Specs:** 0011

## Goal

Helm templates for all application components: operator, task-service, AlertManager adapter, K8s Audit adapter, dashboard, and OAuth2 Proxy. Complete `values.yaml` with all configurable defaults including image tags.

## Work

- `helm/templates/operator.yaml`: operator `Deployment` + `ServiceAccount` + `ClusterRole` + `ClusterRoleBinding`
- `helm/templates/task-service.yaml`: task-service `Deployment` + `Service` + `ConfigMap` for DB connection
- `helm/templates/alertmanager-adapter.yaml`: adapter `Deployment` + `Service`
- `helm/templates/audit-adapter.yaml`: adapter `Deployment` + `Service`
- `helm/templates/dashboard.yaml`: dashboard `Deployment` + `Service`
- `helm/templates/oauth2-proxy.yaml`: OAuth2 Proxy `Deployment` + `Service` + `Ingress`
- `helm/values.yaml` (completed):
  ```yaml
  images:
    operator: ghcr.io/kape-io/operator:0.1.0
    taskService: ghcr.io/kape-io/task-service:0.1.0
    alertmanagerAdapter: ghcr.io/kape-io/alertmanager-adapter:0.1.0
    auditAdapter: ghcr.io/kape-io/audit-adapter:0.1.0
    dashboard: ghcr.io/kape-io/dashboard:0.1.0
  llm:
    provider: openai
    apiKeySecret: ""  # required
  ```

## Acceptance Criteria

- `helm install kape ./helm --set llm.apiKeySecret=my-secret -n kape-system` on fresh kind cluster: all pods Running within 5 minutes
- `helm uninstall kape` cleanly removes all application resources

## Key Files

- `helm/templates/operator.yaml` (new)
- `helm/templates/task-service.yaml` (new)
- `helm/templates/alertmanager-adapter.yaml` (new)
- `helm/templates/audit-adapter.yaml` (new)
- `helm/templates/dashboard.yaml` (new)
- `helm/templates/oauth2-proxy.yaml` (new)
- `helm/values.yaml` (updated — image tags + llm config)
