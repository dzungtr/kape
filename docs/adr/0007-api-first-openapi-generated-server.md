# API-first: OpenAPI YAML is the source of truth, the Chi server is generated

## Status

accepted

## Context

The service exposes an HTTP API consumed by handlers and the dashboard, and the team needed a single authoritative definition of the API surface rather than letting code drift from documentation. Keeping the contract and implementation in sync by hand is error-prone.

## Decision

`openapi/openapi.yaml` is the source of truth, written first; `openapi-generator-cli` with the `go-chi-server` template generates the Chi router and model types into `internal/interfaces/http/gen/`, which is never hand-edited.

## Consequences

API changes must flow through the YAML and regeneration, guaranteeing the contract and server stay aligned, but it imposes a code-generation step in the build and forbids editing generated files, requiring implementations to be fixed after regeneration.

## Source

- [2026-04-05-phase3-task-service-design.md](../../superpowers/specs/2026-04-05-phase3-task-service-design.md)
