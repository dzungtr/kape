# Phase 8.4 — Secret Management

**Status:** pending
**Phase:** 08-audit-security
**Milestone:** M4
**Specs:** 0007

## Goal

Provide External Secrets Operator (ESO) example manifests for managing KapeTool connection secrets. Update the operator to mount KapeTool connection Secrets as files instead of environment variables.

## Work

- `examples/eso/secretstore.yaml`: ESO `SecretStore` pointing to a Vault/AWS SM backend (use Vault as example)
- `examples/eso/externalsecret.yaml`: `ExternalSecret` that pulls the Qdrant connection string and creates a K8s Secret named `kape-tool-<name>-conn`
- Update `operator/infra/k8s/deployment.go`: mount KapeTool connection Secrets as files under `/etc/kape/secrets/<tool-name>/` instead of injecting as env vars
  - `QDRANT_URL` → file at `/etc/kape/secrets/<tool-name>/qdrant_url`
  - `QDRANT_COLLECTION` → file at `/etc/kape/secrets/<tool-name>/qdrant_collection`
- Update `runtime/memory.py` (from 7.4): read from files if env vars absent

## Acceptance Criteria

- `kubectl apply -f examples/eso/` succeeds on a cluster with ESO installed
- Handler Deployment shows volume mount at `/etc/kape/secrets/` not env var injection for tool secrets
- Runtime reads Qdrant connection from file path correctly

## Key Files

- `examples/eso/secretstore.yaml` (new)
- `examples/eso/externalsecret.yaml` (new)
- `operator/infra/k8s/deployment.go` (modified — file mounts)
