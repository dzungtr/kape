# Bulk status updates are best-effort with no DB-level transition validation

## Status

accepted

## Context

The bulk update endpoint receives an operator-selected list of task IDs that may include IDs that no longer exist or that the dashboard has already filtered. A choice was needed on how to handle missing IDs and whether to validate status transitions at the data layer.

## Decision

Non-existent IDs are silently skipped via a single SQL `UPDATE ... WHERE id = ANY($2) RETURNING id`, the response returns only the IDs actually updated, and no transition validation is performed at the DB level because the dashboard only sends pre-filtered valid IDs.

## Consequences

Avoids per-row round trips and keeps the data layer simple, but pushes transition-validity responsibility onto the dashboard client. An all-missing request returns 200 with an empty `affected_ids` list rather than an error, so callers must inspect `affected_ids` to learn what changed. `completed_at` is set automatically in the same statement for terminal statuses.

## Source

- [2026-04-08-bulk-update-status-design.md](../../superpowers/specs/2026-04-08-bulk-update-status-design.md)
