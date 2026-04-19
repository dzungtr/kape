# Phase 8.5 — Immutable Audit Log

**Status:** pending
**Phase:** 08-audit-security
**Milestone:** M4
**Specs:** 0007, 0008

## Goal

Prevent mutation of terminal-state Task records in PostgreSQL by creating an INSERT-only `kape_writer` role and a trigger that blocks UPDATE on rows in terminal states.

## Work

- New migration `task-service/migrations/004_immutable_audit.sql`:
  ```sql
  -- Role with INSERT-only on tasks
  CREATE ROLE kape_writer;
  GRANT INSERT ON tasks TO kape_writer;
  -- Trigger: block UPDATE on terminal-state rows
  CREATE OR REPLACE FUNCTION prevent_terminal_update()
  RETURNS TRIGGER AS $$
  BEGIN
    IF OLD.status IN ('completed', 'failed', 'low_confidence') THEN
      RAISE EXCEPTION 'Cannot update terminal-state task %', OLD.id;
    END IF;
    RETURN NEW;
  END;
  $$ LANGUAGE plpgsql;

  CREATE TRIGGER immutable_terminal_tasks
    BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION prevent_terminal_update();
  ```
- Update task-service connection config to use `kape_writer` role for write operations
- Existing `PATCH /tasks/{id}/status` transitions must all complete before terminal state; no update needed after `completed`/`failed`/`low_confidence`

## Acceptance Criteria

- `PATCH /tasks/{id}/status` to `completed` succeeds
- Subsequent `PATCH /tasks/{id}/status` to any value → HTTP 409 / DB exception
- `INSERT` as `kape_writer` succeeds; `UPDATE` as `kape_writer` fails with permission error

## Key Files

- `task-service/migrations/004_immutable_audit.sql` (new)
