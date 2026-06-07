# Adopt a functional-core / imperative-shell split for reconcilers

## Status

accepted

## Context

The HandlerReconciler grew to 519 lines by intertwining pure decisions (validation, hashing, prompt assembly, condition rollup, dependency gating) with I/O (port calls, requeue logic, status patching). A mechanical file split would reduce line count without addressing the underlying complexity of mixing what the handler should be with how it gets persisted.

## Decision

Move every pure decision into a k8s-free domain package and keep the reconciler as a thin imperative shell that does I/O only, separated by a clean boundary.

## Consequences

Domain logic becomes unit-testable without envtest, and the reconciler shell stays flat and explicit (every k8s-port call appears by name, in spec order, exactly once). Total line count grows because of the wrapper surface, but every line of growth is pure and testable. Establishes the pattern future reconcilers (Schema, Skill, Tool, KapeProxy) will follow.

## Source

- [2026-05-10-handler-reconciler-domain-decomposition-design.md](../../superpowers/specs/2026-05-10-handler-reconciler-domain-decomposition-design.md)
