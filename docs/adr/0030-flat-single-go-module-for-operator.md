# Use a flat single Go module for all operator reconcilers

## Status

accepted

## Context

Phase 6 adds two new reconcilers and rewrites a third, prompting a choice about whether the operator codebase should be split into multiple Go modules per concern or kept as one. Module boundaries affect dependency management and build complexity.

## Decision

Wire all three reconcilers (KapeTool, KapeSchema, KapeHandler) within a single flat Go module with no module split.

## Consequences

Simplifies dependency management and the build by avoiding inter-module versioning. All reconcilers, ports, and adapters share one `go.mod`, which keeps refactoring across component boundaries cheap but couples their release cadence.

## Source

- [2026-04-19-phase6-full-operator-design.md](../../superpowers/specs/2026-04-19-phase6-full-operator-design.md)
