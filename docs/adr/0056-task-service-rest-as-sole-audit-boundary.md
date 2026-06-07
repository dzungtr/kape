# Treat the task-service REST API as the sole audit boundary; postgres reachable only from it

## Status

accepted

## Context

Audit-log integrity depends on no component reading or writing the database except through the task-service REST API. If handler, dashboard, or operator pods could reach postgres directly, the audit boundary would leak.

## Decision

Postgres ingress accepts connections only from `kape.io/component: task-service` on 5432, and task-service ingress accepts only handler (writes) and dashboard (reads) pods on 8080, making the REST API the complete audit boundary per the v1 single-accessor model.

## Consequences

All audit reads and writes funnel through one auditable API surface, enforcing the v1 isolation guarantee. Any future direct-database access pattern (analytics, migrations from other pods) would violate this boundary and require an explicit policy change.

## Source

- [2026-05-17-phase8-issue-77-network-policy-design.md](../../superpowers/specs/2026-05-17-phase8-issue-77-network-policy-design.md)
