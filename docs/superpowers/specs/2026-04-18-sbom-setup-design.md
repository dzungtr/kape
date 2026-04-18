---
title: SBOM Generation — Claude Code Pipeline Integration
date: 2026-04-18
status: approved
---

# SBOM Generation — Claude Code Pipeline Integration

## Overview

Integrate CycloneDX SBOM generation into the Claude Code development workflow so that every PR raised by Claude automatically includes a dependency inventory summary. This complements the existing Snyk GitHub integration (which handles vulnerability scanning) by adding a portable, standards-based SBOM artifact surfaced at review time.

## Context

kape-io is a Go workspace with three modules:
- `adapters`
- `operator`
- `task-service`

Snyk's GitHub integration is already connected to the repo and performs SCA vulnerability scanning on `operator/go.mod` automatically on push. This design does not replace or duplicate that capability.

## Goals

- Generate a per-module CycloneDX 1.4 JSON SBOM for `adapters`, `operator`, and `task-service` whenever Claude raises a PR
- Post a markdown summary of each SBOM as a PR comment so reviewers have dependency visibility
- No generated SBOM files committed to git (derived artifacts; `go.mod`/`go.sum` are the source of truth)
- No Makefile target or manual invocation required

## Non-Goals

- SBOM generation for the Python runtime or Node dashboard (Go modules only for now)
- Uploading SBOM artifacts to Snyk or any external registry
- Replacing the existing Snyk GitHub vulnerability scanning

## Design

### Trigger

A project-level CLAUDE.md instruction that requires Claude to run SBOM generation before creating any PR. This is a behavioral instruction — not a shell hook — so it executes within Claude's own context using the already-authenticated Snyk MCP.

### SBOM Generation

Claude calls `snyk_sbom_scan` MCP tool three times, once per module:
- Path: `./adapters`
- Path: `./operator`
- Path: `./task-service`
- Format: CycloneDX 1.4 JSON

### PR Comment

After generating all three SBOMs, Claude posts a single PR comment via `gh pr comment` containing a markdown summary with:
- Module name
- Total component count
- Any components flagged by Snyk during generation

### Output Persistence

SBOM JSON files are written to a temporary `sbom/` directory during generation and discarded after the PR comment is posted. They are not committed. `sbom/` is added to `.gitignore` to prevent accidental commits.

## CLAUDE.md Instruction

Add to the project-level `CLAUDE.md`:

```
## PR Checklist

Before creating any PR:
1. Run `snyk_sbom_scan` on each Go module (`./adapters`, `./operator`, `./task-service`)
2. Post a markdown summary of all three SBOMs as a PR comment using `gh pr comment`
```

## File Changes

| File | Change |
|---|---|
| `CLAUDE.md` | Add PR checklist with SBOM instruction |
| `.gitignore` | Add `sbom/` entry |

## Success Criteria

- Every PR raised by Claude includes an SBOM summary comment listing components for all three modules
- No `.cdx.json` files appear in `git status` after PR creation
- Existing Snyk GitHub vulnerability scanning is unaffected
