# Treat SBOMs as ephemeral derived artifacts, not committed to git

## Status

accepted

## Context

SBOMs are derived from `go.mod`/`go.sum`, which remain the source of truth for dependencies. Committing generated SBOM files would create redundant, drift-prone artifacts in the repository.

## Decision

Write SBOM JSON to a temporary `sbom/` directory, discard it after posting the PR comment, and add `sbom/` to `.gitignore` to prevent accidental commits.

## Consequences

The repo stays free of generated dependency files and avoids drift between committed SBOMs and actual dependencies. Historical SBOMs are not retained in git; the PR comment becomes the durable review-time record of dependency state.

## Source

- [2026-04-18-sbom-setup-design.md](../../superpowers/specs/2026-04-18-sbom-setup-design.md)
