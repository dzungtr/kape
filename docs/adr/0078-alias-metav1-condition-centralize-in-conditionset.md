# Alias metav1.Condition and centralize condition handling in ConditionSet

## Status

accepted

## Context

Conditions and the Ready rollup were handled by hand-rolled helpers (`findCond`, `isConditionTrue`, `isReady`, `setCondition`, `computeReadyRollup`) duplicated across reconcilers, including a DuplicateDecl linter issue. `metav1.Condition` is the stable industry convention used by every Kubernetes controller.

## Decision

Alias `Condition = metav1.Condition` (no wrapper struct) and provide a domain `ConditionSet` with `Set`/`Find`/`IsTrue`/`ReadyRollup` built on `meta.IsStatusConditionTrue` / `meta.FindStatusCondition`, replacing all hand-rolled equivalents in production and test code.

## Consequences

Avoids taxing every adapter with translation to a domain-private condition shape while still centralizing upsert and the negative-form Ready rollup. Removing the hand-rolled helpers entirely (rather than centralizing them) resolves the DuplicateDecl issue at its root and prevents reintroduction.

## Source

- [2026-05-10-handler-reconciler-domain-decomposition-design.md](../../superpowers/specs/2026-05-10-handler-reconciler-domain-decomposition-design.md)
