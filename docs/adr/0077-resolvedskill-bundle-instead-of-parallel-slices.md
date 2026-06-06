# Use a ResolvedSkill bundle instead of parallel slices for tool resolution

## Status

accepted

## Context

`Handler.ResolveTools` must validate handler-direct tools plus tools pulled in by each skill. Passing skills and their tools as parallel slices (`skills []*Skill, skillTools [][]*Tool`) is a recurring source of mis-zip bugs the type system cannot catch when a fetcher drops an element from one slice but not the other.

## Decision

Define a `ResolvedSkill` value type bundling a Skill with its fetched Tools, and have `ResolveTools` take `[]ResolvedSkill` so mis-pairing is impossible by construction.

## Consequences

The fetcher builds each `ResolvedSkill` inside a single per-skill loop, removing the parallel-slice maintenance window. The signature is self-documenting and the standalone `skills` parameter is dropped as recoverable from resolved. Eliminates the `unionToolMap`/`sortedToolsByName`/map trio it replaces.

## Source

- [2026-05-10-handler-reconciler-domain-decomposition-design.md](../../superpowers/specs/2026-05-10-handler-reconciler-domain-decomposition-design.md)
