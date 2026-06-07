# task-service is the exclusive mediator to PostgreSQL

## Status

accepted

## Context

Handler pods (writers) and the dashboard (reader) both need access to KAPE Task records, and uncontrolled direct database access from multiple components would fragment the persistence contract and couple consumers to the schema. A single boundary is needed to own task persistence and streaming.

## Decision

`kape-task-service` is the sole component that connects to PostgreSQL; all reads and writes from handlers and the dashboard go through its API.

## Consequences

This centralizes the persistence contract, schema enforcement (e.g. terminal-state immutability), and streaming in one service, but makes the task-service a hard dependency and potential bottleneck for every task read/write path.

## Source

- [2026-04-05-phase3-task-service-design.md](../../superpowers/specs/2026-04-05-phase3-task-service-design.md)
