# Use CycloneDX 1.4 JSON as the SBOM format

## Status

accepted

## Context

kape-io needs a dependency inventory artifact surfaced at PR review time to complement Snyk's existing vulnerability scanning. A portable, standards-based format was required so the SBOM is not tied to any single vendor or tool.

## Decision

Generate per-module SBOMs in CycloneDX 1.4 JSON format for the adapters, operator, and task-service Go modules.

## Consequences

Reviewers get a vendor-neutral, machine-readable dependency inventory that can be consumed by downstream tooling. The 1.4 schema is fixed across all three modules, ensuring consistent comment summaries; adopting a newer CycloneDX version later would require updating the generation calls.

## Source

- [2026-04-18-sbom-setup-design.md](../../superpowers/specs/2026-04-18-sbom-setup-design.md)
