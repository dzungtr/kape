# Timeout is an operator-driven UI decision, not a background sweep

## Status

accepted

## Context

The bulk-status endpoint originally ran an automatic age-based sweep, marking all Processing tasks older than N seconds as Timeout. This contradicted the spec, which treats timeout as a human judgment call rather than an automated background job.

## Decision

The dashboard fetches Processing tasks ordered by `received_at` and computes elapsed time client-side; the operator selects specific task IDs and submits them via `PATCH /tasks/bulk/status` with an explicit target status, replacing the age-based `BulkTimeout` with a generic operator-supplied `BulkUpdateStatus`.

## Consequences

Eliminates background timeout jobs and the silently-ignored status field, giving operators direct control over which stuck tasks get marked. The endpoint becomes a general-purpose bulk status setter usable for any status, not just Timeout. The dashboard now owns the logic for deciding when elapsed time indicates a stuck task.

## Source

- [2026-04-08-bulk-update-status-design.md](../../superpowers/specs/2026-04-08-bulk-update-status-design.md)
