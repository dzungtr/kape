# Architecture Decision Records

This directory holds **ADRs** — short records of architectural decisions: *what* was decided and *why*, for choices that are hard to reverse, surprising without context, and the result of a real trade-off.

## ADR vs. spec

- **ADR** (`docs/adr/`) — a decision. Small, focused, records the choice and its rationale. Once accepted it is not rewritten; if reversed, a new ADR supersedes it.
- **Design spec** (`docs/specs/`, `docs/superpowers/specs/`) — long-form, living design documents with status and changelogs. The rationale ADRs link back to.

Don't convert specs into ADRs wholesale. Extract the *settled decisions* into ADRs; leave the design narrative in the specs.

## Format

`NNNN-slug.md`, sequential. An ADR can be a single paragraph — see `0000-template.md`. Add `Status`, `Considered Options`, or `Consequences` only when they earn their place.

## Numbering

Scan this directory for the highest number and increment.
