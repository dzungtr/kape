# Ship ESO integration as reference example manifests, not operator-generated resources

## Status

accepted

## Context

Teams want to source KapeTool secrets from an external vault rather than hand-crafting Kubernetes Secrets, raising the question of whether the operator should own external secret infrastructure.

## Decision

Provide External Secrets Operator SecretStore/ExternalSecret manifests as reference examples under `examples/eso/` (using Vault as the backend example), following the same pattern as the NetworkPolicy and RBAC reference manifests; the operator does not generate or own them.

## Consequences

Keeps the operator out of external secret infrastructure ownership and lets teams adapt the provider stanza to AWS Secrets Manager or GCP Secret Manager. The `ExternalSecret` `target.name` must match the operator's expected Secret name `kape-tool-<name>-conn` for the binding to work.

## Source

- [2026-05-17-phase8-issue-79-secret-management-design.md](../../superpowers/specs/2026-05-17-phase8-issue-79-secret-management-design.md)
