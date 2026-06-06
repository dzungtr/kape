# Surface SBOM summaries as a single PR comment

## Status

accepted

## Context

Reviewers need dependency visibility at review time, and the SBOM files themselves are discarded after generation. A delivery channel was needed that puts the inventory in front of reviewers without committing files.

## Decision

After generating all three SBOMs, post a single PR comment via `gh pr comment` with a markdown summary per module covering module name, total component count, and any Snyk-flagged components.

## Consequences

Reviewers see dependency inventory inline on the PR without leaving GitHub or fetching artifacts. Consolidating into one comment avoids noise, but the summary is limited to component counts and flags rather than the full SBOM detail.

## Source

- [2026-04-18-sbom-setup-design.md](../../superpowers/specs/2026-04-18-sbom-setup-design.md)
