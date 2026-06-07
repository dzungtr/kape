# Issue two role-scoped client certificates instead of one shared cert

## Status

accepted

## Context

mTLS authorization needs to distinguish adapter permissions (publish-only) from handler permissions (subscribe+publish), but a single shared client certificate has only one CN and therefore cannot express a permission split via NATS CN-to-permission mapping.

## Decision

Create exactly two client Certificate resources, `kape-adapter-cert` (CN `kape-adapter`, shared by all adapter Deployments) and `kape-handler-cert` (CN `kape-handler`, shared by all handler Pods), rather than one cert per workload or one shared cert.

## Consequences

CN-based NATS authorization can grant adapters publish-only and handlers subscribe+publish. cert-manager rotates both Secrets automatically before expiry. The CN values become a contract that must exactly match the `authorization.users` entries in the NATS config.

## Source

- [2026-05-17-phase8-issue-81-mtls-nats-design.md](../../superpowers/specs/2026-05-17-phase8-issue-81-mtls-nats-design.md)
