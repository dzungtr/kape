# Pass resolution-time data through method arguments, not receiver state

## Status

accepted

## Context

Skill-consuming methods on `Handler` (`LazySkills`, `SystemPrompt`, `RolloutHash`, `ResolveTools`) need resolved skills. A stateful alternative (`AdoptResolvedSkills` storing skills on a side-field plus a parameter-less `HasLazySkills`) would introduce a hidden lifecycle that is easy to forget and produces confusing test failures.

## Decision

Pass resolution-time data (skills, tools) through method arguments uniformly, including `HasLazySkills(skills []*Skill)`; reserve the wrapper's inner field exclusively for CRD state the reconciler mutates for status writes.

## Consequences

Keeps every skill-consuming method consistent and free of hidden ordering requirements. The reconciler binds the resolved skills once after `ResolveSkills` and passes the same local everywhere, making the single-source pattern testable and avoiding state drift.

## Source

- [2026-05-10-handler-reconciler-domain-decomposition-design.md](../../superpowers/specs/2026-05-10-handler-reconciler-domain-decomposition-design.md)
