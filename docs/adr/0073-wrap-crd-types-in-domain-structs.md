# Wrap CRD types in domain structs instead of type-defining

## Status

accepted

## Context

Domain behaviour needs to attach to the v1alpha1 CRD types while preserving an encapsulation boundary. A type-define (`type Handler v1alpha1.KapeHandler`) would expose every underlying field (Spec, Status, ObjectMeta) directly, letting callers reach past methods and leaving no single interception point for future invariants.

## Decision

Wrap each CRD type in a struct holding an unexported inner pointer (`Handler struct{ inner *v1alpha1.KapeHandler }`) with a `NewHandler` constructor and a single `Raw()` escape hatch; apply the same pattern to Schema, Tool, and Skill.

## Consequences

Forces all access through methods or the one named `Raw()` escape hatch used to hand the underlying object to ports. The zero-cost-conversion benefit of type-define is forgone, but wrapping happens once per reconcile cycle, not in a hot loop, so the cost is negligible.

## Source

- [2026-05-10-handler-reconciler-domain-decomposition-design.md](../../superpowers/specs/2026-05-10-handler-reconciler-domain-decomposition-design.md)
