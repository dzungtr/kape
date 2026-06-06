# Structure pending roadmap phases as per-concern iteration files

## Status

accepted

## Context

Phases 6-10 of the kape-io roadmap had been shipped as massive PRs of 20-80 file changes each, making them hard to review and internalize. A repeatable convention was needed to keep future work reviewable while leaving already-completed phases stable.

## Decision

Keep completed phases 1-5 as flat untouched `.md` files, and convert each pending phase (6-10) into a subdirectory holding a `README.md` (goal, milestone gate, reference specs) plus numbered iteration files, one logical concern per file, each mapping to a single PR of fewer than 20 file changes.

## Consequences

This enforces one-concern-per-PR reviewability and a predictable folder layout under `docs/roadmap/phases/`, but adds indirection (a directory + README per phase) and requires discipline to keep each iteration file scoped to a single PR with goal, work items, acceptance criteria, and key files.

## Source

- [2026-04-20-roadmap-iteration-breakdown-design.md](../../superpowers/specs/2026-04-20-roadmap-iteration-breakdown-design.md)
