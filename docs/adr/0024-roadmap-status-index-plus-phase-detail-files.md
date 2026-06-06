# Use a thin status index plus per-phase detail files for the roadmap

## Status

accepted

## Context

A single monolithic roadmap document grows unbounded and becomes hard to scan as phases accumulate detail (goals, work items, acceptance criteria, key files). The team needed a roadmap that stays readable at a glance while still holding full per-phase depth.

## Decision

Split the roadmap into `docs/roadmap/phases.md` as a small status-index table (Phase, Name, Status, Milestone, Specs, File) and one detail file per phase under `docs/roadmap/phases/XX-name.md` carrying the full goal and acceptance criteria.

## Consequences

The status index stays small forever and gives a fast overview, while detail lives in bounded per-phase files. This constrains contributors to update two coupled locations (the index row and the phase file) on each change, with status limited to the values `done | in-progress | pending`.

## Source

- [2026-04-19-docs-restructure-design.md](../../superpowers/specs/2026-04-19-docs-restructure-design.md)
