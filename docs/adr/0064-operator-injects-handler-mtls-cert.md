# Inject the handler mTLS cert into handler Pods via the operator

## Status

accepted

## Context

Handler Pods are created dynamically by the KAPE operator rather than from static manifests, so their mTLS certificate must be supplied at Pod-creation time rather than declared by an engineer.

## Decision

Have the operator inject the `kape-handler-cert` Secret as a volume mounted at `/etc/kape/nats-certs` plus the three `NATS_TLS` env vars into every handler Deployment it builds, using a named constant for the secret name.

## Consequences

Handler Pods automatically receive mTLS credentials with no per-handler configuration. The `kape-handler-cert` secret name becomes a cluster-scoped contract shared between the operator code and the handler Certificate manifest. This aligns the operator-built handler with the adapter cert-mounting pattern.

## Source

- [2026-05-17-phase8-issue-81-mtls-nats-design.md](../../superpowers/specs/2026-05-17-phase8-issue-81-mtls-nats-design.md)
