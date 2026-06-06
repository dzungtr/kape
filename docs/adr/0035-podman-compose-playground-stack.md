# Use podman compose to run the playground infra and runtime stack

## Status

accepted

## Context

The playground must stand up NATS, PostgreSQL, Qdrant, a stub MCP server, task-service, runtime, and dashboard together so developers can exercise the full handler execution flow locally.

## Decision

Define a single `playground/docker-compose.playground.yml` that runs both the infrastructure layer (nats, postgres, qdrant, stub-mcp) and the runtime layer (task-service, runtime, dashboard), launched via podman compose through Makefile targets.

## Consequences

One command brings up the entire non-operator stack with pinned images. The operator is deliberately excluded from compose because it uses the envtest harness, creating a two-process startup model (compose + separate operator binary).

## Source

- [2026-05-03-local-dev-playground-design.md](../../superpowers/specs/2026-05-03-local-dev-playground-design.md)
