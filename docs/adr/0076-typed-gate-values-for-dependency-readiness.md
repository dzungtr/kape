# Return typed gate values for dependency readiness

## Status

accepted

## Context

When a dependency is not ready, the reconciler must record a `DependenciesReady=False` condition with the correct reason and message, replacing four scattered inline `setCondition` calls. The readiness checks for skills, tools, and schema fail for distinct reasons (`KapeSkillNotFound`/`NotReady`, `KapeToolNotReady`, `KapeSchemaInvalid`).

## Decision

Have entity methods return distinct plain-value gate structs (`SkillGate`, `ToolGate`, `SchemaGate`), each with `OK`/`Reason`/`Message` and an `AsCondition()` builder, while the reconciler writes them to status via `recordGateAndRequeue`.

## Consequences

Centralizes condition construction in `AsCondition()`, removing the four scattered inline condition literals. Cleanly separates gating policy (domain) from gating I/O (shell). Distinct gate types document the distinct failure reasons at the type level.

## Source

- [2026-05-10-handler-reconciler-domain-decomposition-design.md](../../superpowers/specs/2026-05-10-handler-reconciler-domain-decomposition-design.md)
