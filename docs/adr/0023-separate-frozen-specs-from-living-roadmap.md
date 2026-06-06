# Separate frozen design specs from the living roadmap

## Status

accepted

## Context

The root `specs/` directory mixed two documents with different lifecycles: frozen per-session design specs (immutable decisions) and a v1 roadmap (planning state that evolves every session). Co-locating them created a category error where mutable build sequencing lived alongside immutable design records.

## Decision

Keep immutable design specs under `docs/specs/NNNN-topic/` (frozen, referenced by the roadmap) and move the living build sequence into a separate `docs/roadmap/` tree, retaining the old `0012-v1-roadmap` spec only as an archived baseline that points to `docs/roadmap/`.

## Consequences

Each hydration cycle can evolve the roadmap without touching frozen specs, and specs remain stable references. The roadmap becomes the single source of truth for build status, while the archived 0012 baseline must carry a deprecation note to avoid being mistaken for the live roadmap.

## Source

- [2026-04-19-docs-restructure-design.md](../../superpowers/specs/2026-04-19-docs-restructure-design.md)
