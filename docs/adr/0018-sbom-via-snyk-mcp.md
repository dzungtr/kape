# Generate SBOMs via the Snyk MCP tool inside Claude's context

## Status

accepted

## Context

SBOMs must be produced as part of the Claude Code PR workflow without requiring a Makefile target or manual invocation. Snyk's MCP is already authenticated in Claude's environment.

## Decision

Claude calls the `snyk_sbom_scan` MCP tool once per Go module (`./adapters`, `./operator`, `./task-service`) to produce the SBOMs.

## Consequences

Generation reuses the existing authenticated Snyk MCP, avoiding new CI plumbing or credentials. It couples SBOM generation to the availability of the Snyk MCP in Claude's session rather than a standalone CLI invocation.

## Source

- [2026-04-18-sbom-setup-design.md](../../superpowers/specs/2026-04-18-sbom-setup-design.md)
