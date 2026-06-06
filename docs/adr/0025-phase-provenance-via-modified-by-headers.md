# Track phase changes via in-file "Modified by" headers instead of changelog files

## Status

accepted

## Context

As successive hydration sessions modify roadmap phases, the team needed traceability of which spec introduced which change without accumulating separate hydration changelog artifacts that would themselves need maintenance.

## Decision

Record provenance directly in each phase file via a `Modified by:` header updated in-place each cycle (e.g. "0012 (created), 0013 (KapeSkill added)"), relying on git history for the actual diffs.

## Consequences

No separate changelog files are needed, and every phase file is self-describing about its lineage. This depends on contributors faithfully updating the `Modified by:` line during each hydration cycle, since the header is the human-readable record while git supplies the detailed diff.

## Source

- [2026-04-19-docs-restructure-design.md](../../superpowers/specs/2026-04-19-docs-restructure-design.md)
