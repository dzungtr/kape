# Use a single NATS subject per producer for audit events

## Status

accepted

## Context

The K8s API server emits audit events across many verbs and resources, and an earlier iteration file proposed decomposing the NATS subject into `kape.events.audit.<verb>.<resource>`. This conflicts with the locked one-subject-per-producer principle in spec 0006, raising the question of how handlers should select among audit events.

## Decision

Publish all audit events to the single subject `kape.events.security.audit`, and let handlers select signals via `trigger.filter.jsonpath` on fields like `$.data.verb` and `$.data.resource` rather than via subject-name decomposition.

## Consequences

Subjects stay stable and producer-scoped, so the `KAPE_EVENTS` stream's `kape.events.>` filter captures all audit events without per-verb subject sprawl. Intra-producer selectivity becomes a handler-config concern, keeping routing logic out of the adapter and consistent across all producers.

## Source

- [2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md](../../superpowers/specs/2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md)
