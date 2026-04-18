# Design: Fix BulkUpdateStatus to Match Spec

**Date:** 2026-04-08
**Status:** Approved
**Branch:** feature/phase3-task-service

---

## Context

The current `PATCH /tasks/bulk/status` implementation does an automatic age-based sweep — it marks all `Processing` tasks older than N seconds as `Timeout`. This does not match the spec (section 7.6 of `specs/0004-kape-handler/README.md`).

The spec says:
> Timeout is a UI concern — no background jobs. The dashboard fetches all `Processing` tasks ordered by `received_at ASC` and computes elapsed time client-side. The operator decides when elapsed time indicates a stuck task and manually marks it `Timeout` via the dashboard.

The endpoint is meant to let the dashboard submit an operator-selected list of task IDs to mark with a given status.

Additional problems in the current implementation:
- `BulkTimeoutRequest.status` field is accepted but silently ignored
- The command is named `BulkTimeoutCommand` despite handling a generic bulk update

---

## Design

### API Change

Replace `BulkTimeoutRequest` with `BulkUpdateStatusRequest`:

```yaml
BulkUpdateStatusRequest:
  type: object
  required: [ids, status]
  properties:
    ids:
      type: array
      items:
        type: string
      minItems: 1
    status:
      $ref: '#/components/schemas/TaskStatus'
```

Response schema (`BulkUpdateStatusResponse`) stays the same — returns `affected_ids: [string]`.

The endpoint `PATCH /tasks/bulk/status` accepts the new body. Non-existent IDs are silently skipped (best-effort semantics). The response contains only the IDs that were actually updated.

### Repository Port

Replace:
```go
BulkTimeout(ctx context.Context, olderThanSeconds int) ([]string, error)
```
with:
```go
BulkUpdateStatus(ctx context.Context, ids []string, status TaskStatus) ([]string, error)
```

### Postgres Implementation

Single SQL statement — no per-row round trips:
```sql
UPDATE tasks
SET
  status = $1,
  completed_at = CASE WHEN $1 IN ('Timeout','Done','Failed','SchemaValidationFailed','ActionError','UnprocessableEvent','Retried')
                      THEN NOW() ELSE NULL END
WHERE id = ANY($2)
RETURNING id
```

Non-existent IDs are skipped naturally. No transition validation at DB level — the dashboard only sends IDs it has already filtered to valid states.

### Application Command

Rename `BulkTimeoutCommand` → `BulkUpdateStatusCommand`.

Input:
```go
type BulkUpdateStatusInput struct {
    IDs    []string
    Status task.TaskStatus
}
```

The command calls `repo.BulkUpdateStatus`, then publishes an SSE event for each updated ID.

### HTTP Handler

`BulkUpdateStatus` reads `ids` and `status` from the new request body, passes them to the command.

### Error Handling

- Empty `ids` array → 400 Bad Request (enforced by OpenAPI `minItems: 1`)
- Invalid `status` value → 400 Bad Request
- All IDs not found → 200 with empty `affected_ids` (best-effort, no error)
- DB error → 500

### Mock

Regenerate mockery mock for `task.Repository` after changing the port interface.

### Tests

| Layer | Change |
|-------|--------|
| Unit (`bulk_timeout_test.go`) | Rename file, update mock call to `BulkUpdateStatus(ids, status)` |
| Integration (`task_repository_test.go`) | Replace `BulkTimeout` test with `BulkUpdateStatus` test covering: found IDs updated, missing IDs skipped, `completed_at` set for terminal status |
| E2E (`tasks_test.go`) | Replace `TestE2E_BulkTimeout` — send specific IDs, assert only those are updated |

---

## Files Affected

| File | Change |
|------|--------|
| `task-service/openapi/openapi.yaml` | Replace `BulkTimeoutRequest` schema and regenerate `gen/` |
| `task-service/internal/domain/task/repository.go` | Replace `BulkTimeout` with `BulkUpdateStatus` |
| `task-service/internal/domain/task/mocks/mock_repository.go` | Regenerate |
| `task-service/internal/infrastructure/postgres/task_repository.go` | Replace `BulkTimeout` method |
| `task-service/internal/infrastructure/postgres/task_repository_test.go` | Update integration test |
| `task-service/internal/application/command/bulk_timeout.go` | Rename + rewrite |
| `task-service/internal/application/command/bulk_timeout_test.go` | Update unit test |
| `task-service/internal/interfaces/http/server.go` | Update handler |
| `task-service/internal/interfaces/http/server_test.go` | Update handler test |
| `task-service/test/e2e/tasks_test.go` | Update E2E test |
| `task-service/cmd/task-service/main.go` | Rename command wiring |
