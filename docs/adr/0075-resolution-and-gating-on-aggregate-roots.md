# Locate resolution and gating logic on aggregate roots

## Status

accepted

## Context

Dependency resolution, derivation, and gating need a home. Free-standing `domain.Resolve*`/`Check*` functions would scatter policy across the package surface and obscure ownership of the reconciled aggregate.

## Decision

Place resolution, derivation, and gating as methods on the aggregate roots (`Handler` and the self-contained sub-aggregate `Schema`); Skill, Tool, and Schema-as-input arrive as method arguments, and the reconciler only ever calls `h.<verb>(...)` / `schema.<verb>(...)`.

## Consequences

Gating policy (what is a failure, what reason) lives in the domain while gating I/O (status patch and requeue) stays in the shell via `recordGateAndRequeue`. No standalone resolver/checker functions appear on the public surface; internal helpers like `partitionSkills` stay unexported package-locals.

## Source

- [2026-05-10-handler-reconciler-domain-decomposition-design.md](../../superpowers/specs/2026-05-10-handler-reconciler-domain-decomposition-design.md)
