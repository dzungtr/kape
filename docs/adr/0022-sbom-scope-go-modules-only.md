# Scope SBOM generation to Go modules only

## Status

accepted

## Context

kape-io includes a Python runtime and a Node dashboard in addition to its three Go modules. A boundary was needed to define which parts of the workspace the SBOM pipeline covers initially.

## Decision

Generate SBOMs only for the three Go modules (adapters, operator, task-service); explicitly exclude the Python runtime and Node dashboard for now.

## Consequences

Keeps the initial pipeline simple and aligned with Snyk's existing Go-focused scanning, but leaves Python and Node dependencies without SBOM coverage. Extending coverage later would require adding new generation paths and tooling for those ecosystems.

## Source

- [2026-04-18-sbom-setup-design.md](../../superpowers/specs/2026-04-18-sbom-setup-design.md)
