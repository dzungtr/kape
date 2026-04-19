# Phase 10.1 — Helm Infrastructure Templates

**Status:** pending
**Phase:** 10-helm-polish
**Milestone:** M6
**Specs:** 0011

## Goal

Helm templates for stateful infrastructure: NATS JetStream StatefulSet, CloudNativePG cluster, cert-manager `ClusterIssuer` + `Certificate` resources, and CRD install job.

## Work

- `helm/Chart.yaml`: chart name `kape`, version `0.1.0`, appVersion `0.1.0`
- `helm/values.yaml` (initial): infra-related defaults
  ```yaml
  nats:
    replicas: 3
    storage: 10Gi
  postgres:
    instances: 1
    storage: 20Gi
  certManager:
    enabled: true
    issuerEmail: "admin@example.com"
  ```
- `helm/templates/nats.yaml`: NATS JetStream `StatefulSet` + headless `Service` + `ConfigMap` for nats-server config
- `helm/templates/cnpg.yaml`: CloudNativePG `Cluster` resource
- `helm/templates/certmanager.yaml`: `ClusterIssuer` (Let's Encrypt staging + production) + `Certificate` for NATS server
- `helm/templates/crds.yaml`: `Job` to `kubectl apply -f crds/` on install (or use Helm CRD install hook)

## Acceptance Criteria

- `helm template kape ./helm` renders all infra resources without errors
- `helm install kape ./helm -n kape-system` on fresh kind cluster: NATS pods Running, CNPG cluster Ready
- `helm uninstall kape` removes all resources (no orphaned PVCs without explicit `persistence.retain`)

## Key Files

- `helm/Chart.yaml` (new)
- `helm/values.yaml` (new)
- `helm/templates/nats.yaml` (new)
- `helm/templates/cnpg.yaml` (new)
- `helm/templates/certmanager.yaml` (new)
- `helm/templates/crds.yaml` (new)
