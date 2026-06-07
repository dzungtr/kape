# Embed SQL migrations and run them at startup

## Status

accepted

## Context

The service owns its PostgreSQL schema (enums, partitioned tables, indexes) and needs schema changes applied reliably without a separate manual migration step or external tooling at deploy time. A deterministic migration mechanism tied to the binary was required.

## Decision

Use `golang-migrate` with `embed.FS` to bundle the SQL files into the binary and run migrations automatically at startup before the HTTP server starts, with a `--migrate-only` flag for standalone runs.

## Consequences

The running binary always carries its required schema and applies it on boot, simplifying deployment, but startup is gated on successful migration and the migration ordering is fixed by the embedded numbered files.

## Source

- [2026-04-05-phase3-task-service-design.md](../../superpowers/specs/2026-04-05-phase3-task-service-design.md)
