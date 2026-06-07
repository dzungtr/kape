# Use a single flat domain package with no subpackages

## Status

accepted

## Context

`operator/domain/` existed with empty `handler/`, `tool/`, and `schema/` subdirectories declaring an architectural intent. The four CRD wrappers reference each other (`Handler.SystemPrompt` takes `[]*Skill`, `Handler.DesiredLabels` references `Tool.Name`), so subpackages would force either circular imports or upward extraction into a parent package.

## Decision

Make `operator/domain` a single Go package with no subpackages; organize files by topic (`handler.go`, `schema.go`, `skill.go`, `tool.go`, `conditions.go`) for navigability only, not as architectural boundaries.

## Consequences

Eliminates circular-import pressure between the mutually-referencing wrappers. The empty subdirectories and their `.gitkeep` files are deleted. Topic-based files give navigability without imposing boundaries that the cross-references would fight.

## Source

- [2026-05-10-handler-reconciler-domain-decomposition-design.md](../../superpowers/specs/2026-05-10-handler-reconciler-domain-decomposition-design.md)
