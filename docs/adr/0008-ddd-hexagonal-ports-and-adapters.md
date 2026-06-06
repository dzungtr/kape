# task-service uses DDD hexagonal ports-and-adapters layering

## Status

accepted

## Context

The service mixes HTTP handling, business rules (e.g. state-machine transitions), and persistence/streaming concerns, which would tangle if implemented directly against PostgreSQL and the HTTP framework. A layering discipline was needed to keep domain logic independent of infrastructure.

## Decision

Use Domain-Driven Design with dependency inversion: domain defines `Repository` and `Stream` ports, application commands/queries depend only on interfaces, infrastructure implements the ports, and `main.go` is the sole composition root that wires all layers.

## Consequences

Domain and application logic become testable with mocked interfaces and independent of go-pg/SSE specifics, but the layering adds boilerplate (constructor injection, separate command/query structs) and concentrates all wiring knowledge in `main.go`.

## Source

- [2026-04-05-phase3-task-service-design.md](../../superpowers/specs/2026-04-05-phase3-task-service-design.md)
