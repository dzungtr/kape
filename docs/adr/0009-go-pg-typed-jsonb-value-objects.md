# Persist with go-pg and typed JSONB value objects

## Status

accepted

## Context

Task records include several semi-structured columns (`event_raw`, `schema_output`, `actions`, `error`) that must be stored in PostgreSQL, and using untyped `map[string]interface{}` would lose type safety and require ad-hoc marshalling everywhere. The repository also needs a query approach that avoids scattered raw SQL.

## Decision

Implement the `Repository` with `go-pg/pg/v10`, mapping JSONB columns to typed Go structs via pg struct tags, and route all queries through the go-pg query builder with no raw SQL strings outside migration files.

## Consequences

JSONB columns get compile-time type safety and round-trip testing, and queries stay centralized in the builder, but the service is now coupled to go-pg's ORM semantics and tag conventions for all persistence.

## Source

- [2026-04-05-phase3-task-service-design.md](../../superpowers/specs/2026-04-05-phase3-task-service-design.md)
