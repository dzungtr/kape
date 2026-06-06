# Trigger SBOM generation via a CLAUDE.md instruction, not a shell hook

## Status

accepted

## Context

The pipeline needs SBOM generation to run before every Claude-raised PR, with no manual step. The choice was between a deterministic shell/git hook and a behavioral instruction executed by Claude.

## Decision

Use a project-level `CLAUDE.md` PR-checklist instruction that requires Claude to run SBOM generation before creating any PR, executing within Claude's own context.

## Consequences

Generation runs inside Claude's authenticated context and can use the Snyk MCP directly, but enforcement is behavioral rather than mechanically guaranteed like a hook. Changing the trigger only requires editing `CLAUDE.md`, not pipeline infrastructure.

## Source

- [2026-04-18-sbom-setup-design.md](../../superpowers/specs/2026-04-18-sbom-setup-design.md)
