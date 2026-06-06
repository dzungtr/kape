# Enforce terminal-state immutability in the application layer

## Status

accepted

## Context

Task status follows a state machine where states like Completed and Failed represent final outcomes, and allowing them to transition again would corrupt the audit record and emit misleading events. The system needed a single place to guard valid status transitions.

## Decision

Treat terminal states as never-updated and reject invalid transitions with a domain error enforced in the application layer (returning 409 at the HTTP boundary), rather than relying on database constraints.

## Consequences

Status transitions are validated consistently with clear domain errors and tested transition rules, but the immutability guarantee lives in Go code rather than the database, so any writer bypassing the application layer could violate it.

## Source

- [2026-04-05-phase3-task-service-design.md](../../superpowers/specs/2026-04-05-phase3-task-service-design.md)
