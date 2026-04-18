# BulkUpdateStatus Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the age-based `BulkTimeout` sweep with an operator-driven `BulkUpdateStatus` that accepts explicit task IDs and a target status, matching spec section 7.6.

**Architecture:** Change propagates bottom-up — domain port → postgres → application command → HTTP handler → tests. The OpenAPI spec and its generated types are updated first so downstream code compiles against the new contract.

**Tech Stack:** Go, go-pg v10, openapi-generator (go-chi-server), mockery v2, testcontainers-go

---

### Task 1: Update OpenAPI spec and regenerate gen/

**Files:**
- Modify: `task-service/openapi/openapi.yaml`
- Regenerate: `task-service/internal/interfaces/http/gen/` (whole directory via `make generate`)

- [ ] **Step 1: Update BulkTimeoutRequest schema in openapi.yaml**

In `task-service/openapi/openapi.yaml`, find and replace the `BulkTimeoutRequest` schema (around line 464) and the endpoint summary (around line 111):

Replace the endpoint summary:
```yaml
      summary: Bulk mark Processing tasks as Timeout
```
with:
```yaml
      summary: Bulk update status for operator-selected tasks
```

Replace the schema:
```yaml
    BulkTimeoutRequest:
      type: object
      required: [status, older_than_seconds]
      properties:
        status:
          $ref: '#/components/schemas/TaskStatus'
        older_than_seconds:
          type: integer
          minimum: 1
```
with:
```yaml
    BulkTimeoutRequest:
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

- [ ] **Step 2: Regenerate gen/**

```bash
cd task-service && make generate
```

Expected: generator runs without error, `internal/interfaces/http/gen/model_bulk_timeout_request.go` updated. The generated `BulkTimeoutRequest` struct will now have `Ids []string` and `Status TaskStatus` fields (no more `OlderThanSeconds`).

- [ ] **Step 3: Verify generated struct**

```bash
grep -A8 "BulkTimeoutRequest struct" task-service/internal/interfaces/http/gen/model_bulk_timeout_request.go
```

Expected output (field names may vary slightly by generator version):
```go
type BulkTimeoutRequest struct {
    Ids    []string   `json:"ids"`
    Status TaskStatus `json:"status"`
}
```

- [ ] **Step 4: Commit**

```bash
cd task-service && git add openapi/openapi.yaml internal/interfaces/http/gen/
git commit -m "feat(task-service): update BulkTimeoutRequest schema to accept ids+status"
```

---

### Task 2: Update domain port and regenerate mock

**Files:**
- Modify: `task-service/internal/domain/task/repository.go`
- Regenerate: `task-service/internal/domain/task/mocks/mock_repository.go`

- [ ] **Step 1: Replace BulkTimeout with BulkUpdateStatus in the port**

In `task-service/internal/domain/task/repository.go`, replace:
```go
	BulkTimeout(ctx context.Context, olderThanSeconds int) ([]string, error)
```
with:
```go
	BulkUpdateStatus(ctx context.Context, ids []string, status TaskStatus) ([]string, error)
```

- [ ] **Step 2: Regenerate the mock**

```bash
cd task-service && make mock
```

Expected: `internal/domain/task/mocks/mock_repository.go` regenerated. `BulkTimeout` method removed, `BulkUpdateStatus` method added.

- [ ] **Step 3: Verify the codebase now fails to compile (expected — not yet updated)**

```bash
cd task-service && go build ./... 2>&1 | head -20
```

Expected: compile errors referencing `BulkTimeout` in `task_repository.go`, `bulk_timeout.go`, and `server.go`. This confirms the interface change propagated.

- [ ] **Step 4: Commit**

```bash
git add internal/domain/task/repository.go internal/domain/task/mocks/mock_repository.go
git commit -m "feat(task-service): replace BulkTimeout port with BulkUpdateStatus"
```

---

### Task 3: Update Postgres implementation (TDD)

**Files:**
- Modify: `task-service/internal/infrastructure/postgres/task_repository.go`
- Modify: `task-service/internal/infrastructure/postgres/task_repository_test.go`

- [ ] **Step 1: Write failing integration tests**

In `task-service/internal/infrastructure/postgres/task_repository_test.go`, replace `TestTaskRepository_BulkTimeout` (around line 244) with:

```go
func TestTaskRepository_BulkUpdateStatus_UpdatesMatchingIDs(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	t1 := fixedTask("01BU1")
	t2 := fixedTask("01BU2")
	t3 := fixedTask("01BU3")
	require.NoError(t, repo.Create(ctx, t1))
	require.NoError(t, repo.Create(ctx, t2))
	require.NoError(t, repo.Create(ctx, t3))

	// Update only t1 and t2
	affected, err := repo.BulkUpdateStatus(ctx, []string{"01BU1", "01BU2"}, task.StatusTimeout)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"01BU1", "01BU2"}, affected)

	found1, _ := repo.FindByID(ctx, "01BU1")
	assert.Equal(t, task.StatusTimeout, found1.Status)
	assert.NotNil(t, found1.CompletedAt, "terminal status should set completed_at")

	found2, _ := repo.FindByID(ctx, "01BU2")
	assert.Equal(t, task.StatusTimeout, found2.Status)

	found3, _ := repo.FindByID(ctx, "01BU3")
	assert.Equal(t, task.StatusProcessing, found3.Status, "t3 should be untouched")
}

func TestTaskRepository_BulkUpdateStatus_SkipsMissingIDs(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	t1 := fixedTask("01BU4")
	require.NoError(t, repo.Create(ctx, t1))

	// One real ID, one non-existent
	affected, err := repo.BulkUpdateStatus(ctx, []string{"01BU4", "NOTEXIST"}, task.StatusTimeout)
	require.NoError(t, err)
	assert.Equal(t, []string{"01BU4"}, affected)
}

func TestTaskRepository_BulkUpdateStatus_EmptyIDs_ReturnsEmpty(t *testing.T) {
	db, cleanup := setupDB(t)
	defer cleanup()

	repo := postgres.NewTaskRepository(db)
	ctx := context.Background()

	affected, err := repo.BulkUpdateStatus(ctx, []string{}, task.StatusTimeout)
	require.NoError(t, err)
	assert.Empty(t, affected)
}
```

- [ ] **Step 2: Run tests — confirm they fail**

```bash
cd task-service && go test ./internal/infrastructure/postgres/... -run TestTaskRepository_BulkUpdateStatus -v 2>&1 | tail -20
```

Expected: compile error — `BulkUpdateStatus` not defined on `*TaskRepository`.

- [ ] **Step 3: Implement BulkUpdateStatus in task_repository.go**

In `task-service/internal/infrastructure/postgres/task_repository.go`, replace the `BulkTimeout` method (lines 277–297) with:

```go
func (r *TaskRepository) BulkUpdateStatus(ctx context.Context, ids []string, status task.TaskStatus) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	var rows []struct {
		ID string `pg:"id"`
	}
	var query string
	if status.IsTerminal() {
		query = `UPDATE tasks SET status = ?, completed_at = NOW() WHERE id IN (?) RETURNING id`
	} else {
		query = `UPDATE tasks SET status = ? WHERE id IN (?) RETURNING id`
	}
	_, err := r.db.QueryContext(ctx, &rows, query, status, pg.In(ids))
	if err != nil {
		return nil, err
	}
	result := make([]string, len(rows))
	for i, row := range rows {
		result[i] = row.ID
	}
	return result, nil
}
```

- [ ] **Step 4: Run integration tests — confirm they pass**

```bash
cd task-service && go test ./internal/infrastructure/postgres/... -run TestTaskRepository_BulkUpdateStatus -v 2>&1 | tail -20
```

Expected: all 3 `TestTaskRepository_BulkUpdateStatus_*` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/infrastructure/postgres/task_repository.go internal/infrastructure/postgres/task_repository_test.go
git commit -m "feat(task-service): implement BulkUpdateStatus in postgres repository"
```

---

### Task 4: Rename and rewrite application command (TDD)

**Files:**
- Rename+rewrite: `task-service/internal/application/command/bulk_timeout.go` → `bulk_update_status.go`
- Rename+rewrite: `task-service/internal/application/command/bulk_timeout_test.go` → `bulk_update_status_test.go`

- [ ] **Step 1: Write failing unit tests**

```bash
cd task-service && git mv internal/application/command/bulk_timeout_test.go internal/application/command/bulk_update_status_test.go
```

Replace the entire content of `internal/application/command/bulk_update_status_test.go` with:

```go
package command_test

import (
	"context"
	"testing"

	"github.com/kape-io/kape/task-service/internal/application/command"
	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/kape-io/kape/task-service/internal/domain/task/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBulkUpdateStatusCommand_PublishesForEachAffected(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	stream := mocks.NewMockStream(t)

	repo.On("BulkUpdateStatus", mock.Anything, []string{"01A", "01B"}, task.StatusTimeout).
		Return([]string{"01A", "01B"}, nil)
	stream.On("Publish", mock.MatchedBy(func(tsk *task.Task) bool {
		return tsk.Status == task.StatusTimeout
	})).Return().Times(2)

	cmd := command.NewBulkUpdateStatusCommand(repo, stream)
	ids, err := cmd.Execute(context.Background(), command.BulkUpdateStatusInput{
		IDs:    []string{"01A", "01B"},
		Status: task.StatusTimeout,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"01A", "01B"}, ids)
	stream.AssertNumberOfCalls(t, "Publish", 2)
}

func TestBulkUpdateStatusCommand_NoneAffected_NoPublish(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	stream := mocks.NewMockStream(t)

	repo.On("BulkUpdateStatus", mock.Anything, []string{"01X"}, task.StatusTimeout).
		Return([]string{}, nil)

	cmd := command.NewBulkUpdateStatusCommand(repo, stream)
	ids, err := cmd.Execute(context.Background(), command.BulkUpdateStatusInput{
		IDs:    []string{"01X"},
		Status: task.StatusTimeout,
	})
	require.NoError(t, err)
	assert.Empty(t, ids)
	stream.AssertNotCalled(t, "Publish", mock.Anything)
}
```

- [ ] **Step 2: Run — confirm it fails**

```bash
cd task-service && go test ./internal/application/command/... -run TestBulkUpdateStatus -v 2>&1 | tail -10
```

Expected: compile error — `NewBulkUpdateStatusCommand` undefined.

- [ ] **Step 3: Rename and rewrite the command file**

```bash
git mv task-service/internal/application/command/bulk_timeout.go task-service/internal/application/command/bulk_update_status.go
```

Replace the entire content of `internal/application/command/bulk_update_status.go` with:

```go
package command

import (
	"context"

	"github.com/kape-io/kape/task-service/internal/domain/task"
)

type BulkUpdateStatusInput struct {
	IDs    []string
	Status task.TaskStatus
}

type BulkUpdateStatusCommand struct {
	repo   task.Repository
	stream task.Stream
}

func NewBulkUpdateStatusCommand(repo task.Repository, stream task.Stream) *BulkUpdateStatusCommand {
	return &BulkUpdateStatusCommand{repo: repo, stream: stream}
}

func (c *BulkUpdateStatusCommand) Execute(ctx context.Context, in BulkUpdateStatusInput) ([]string, error) {
	ids, err := c.repo.BulkUpdateStatus(ctx, in.IDs, in.Status)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		c.stream.Publish(&task.Task{ID: id, Status: in.Status})
	}
	return ids, nil
}
```

- [ ] **Step 4: Run tests — confirm they pass**

```bash
cd task-service && go test ./internal/application/command/... -run TestBulkUpdateStatus -v 2>&1 | tail -10
```

Expected: both `TestBulkUpdateStatusCommand_*` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/application/command/
git commit -m "feat(task-service): rename BulkTimeoutCommand to BulkUpdateStatusCommand"
```

---

### Task 5: Update HTTP handler and server tests

**Files:**
- Modify: `task-service/internal/interfaces/http/server.go`
- Modify: `task-service/internal/interfaces/http/server_test.go`

- [ ] **Step 1: Write failing handler test**

In `task-service/internal/interfaces/http/server_test.go`, add this test after `TestServer_CreateTask_201`:

```go
func TestServer_BulkUpdateStatus_200(t *testing.T) {
	srv, repo, stream := buildServer(t)

	repo.On("BulkUpdateStatus", mock.Anything, []string{"01A", "01B"}, task.StatusTimeout).
		Return([]string{"01A", "01B"}, nil)
	stream.On("Publish", mock.Anything).Return()

	body, _ := json.Marshal(map[string]interface{}{
		"ids":    []string{"01A", "01B"},
		"status": "Timeout",
	})
	req := httptest.NewRequest(http.MethodPatch, "/tasks/bulk/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	affected := resp["affected_ids"].([]interface{})
	assert.Len(t, affected, 2)
}
```

- [ ] **Step 2: Run — confirm it fails**

```bash
cd task-service && go test ./internal/interfaces/http/... -run TestServer_BulkUpdateStatus -v 2>&1 | tail -10
```

Expected: compile error — `NewBulkTimeoutCommand` no longer exists / type mismatch.

- [ ] **Step 3: Update server.go**

In `task-service/internal/interfaces/http/server.go`, make these changes:

Replace the `bulkTimeout` field in `Server` struct:
```go
	bulkTimeout  *command.BulkTimeoutCommand
```
with:
```go
	bulkUpdateStatus *command.BulkUpdateStatusCommand
```

Replace the `bulkTimeout` parameter in `NewServer`:
```go
	bulkTimeout *command.BulkTimeoutCommand,
```
with:
```go
	bulkUpdateStatus *command.BulkUpdateStatusCommand,
```

Replace the `bulkTimeout: bulkTimeout` assignment in the `NewServer` return:
```go
		deleteTask: deleteTask, bulkTimeout: bulkTimeout,
```
with:
```go
		deleteTask: deleteTask, bulkUpdateStatus: bulkUpdateStatus,
```

Replace the entire `BulkUpdateStatus` handler (lines 219–234):
```go
// BulkUpdateStatus handles PATCH /tasks/bulk/status
func (s *Server) BulkUpdateStatus(w http.ResponseWriter, r *http.Request) {
	var req gen.BulkTimeoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Ids) == 0 {
		writeError(w, http.StatusBadRequest, "ids must not be empty")
		return
	}
	ids, err := s.bulkUpdateStatus.Execute(r.Context(), command.BulkUpdateStatusInput{
		IDs:    req.Ids,
		Status: task.TaskStatus(req.Status),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, gen.BulkTimeoutResponse{AffectedIds: ids})
}
```

- [ ] **Step 4: Update buildServer in server_test.go**

In `task-service/internal/interfaces/http/server_test.go`, replace:
```go
		command.NewBulkTimeoutCommand(repo, stream),
```
with:
```go
		command.NewBulkUpdateStatusCommand(repo, stream),
```

- [ ] **Step 5: Run handler tests — confirm they pass**

```bash
cd task-service && go test ./internal/interfaces/http/... -run TestServer -v 2>&1 | tail -20
```

Expected: all `TestServer_*` tests PASS including `TestServer_BulkUpdateStatus_200`.

- [ ] **Step 6: Commit**

```bash
git add internal/interfaces/http/server.go internal/interfaces/http/server_test.go
git commit -m "feat(task-service): update HTTP handler for BulkUpdateStatus with ids+status"
```

---

### Task 6: Update E2E test

**Files:**
- Modify: `task-service/test/e2e/tasks_test.go`

- [ ] **Step 1: Update startServer to use new command**

In `task-service/test/e2e/tasks_test.go`, replace:
```go
		command.NewBulkTimeoutCommand(repo, hub),
```
with:
```go
		command.NewBulkUpdateStatusCommand(repo, hub),
```

- [ ] **Step 2: Replace TestE2E_BulkTimeout**

Replace `TestE2E_BulkTimeout` (lines 277–301) with:

```go
func TestE2E_BulkUpdateStatus(t *testing.T) {
	env := startServer(t)

	// Create 3 tasks
	for _, id := range []string{"01BU1", "01BU2", "01BU3"} {
		env.post(t, "/tasks", createTaskBody(id))
	}

	// Operator selects 01BU1 and 01BU2 to mark as Timeout
	resp := env.patch(t, "/tasks/bulk/status", map[string]interface{}{
		"ids":    []string{"01BU1", "01BU2"},
		"status": "Timeout",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var bulkResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&bulkResp)
	affected := bulkResp["affected_ids"].([]interface{})
	assert.Len(t, affected, 2)

	// 01BU1 and 01BU2 should be Timeout
	for _, id := range []string{"01BU1", "01BU2"} {
		r := env.get(t, "/tasks/"+id)
		var got map[string]interface{}
		json.NewDecoder(r.Body).Decode(&got)
		assert.Equal(t, "Timeout", got["status"], "task %s should be Timeout", id)
	}

	// 01BU3 should still be Processing
	r := env.get(t, "/tasks/01BU3")
	var got map[string]interface{}
	json.NewDecoder(r.Body).Decode(&got)
	assert.Equal(t, "Processing", got["status"], "task 01BU3 should be untouched")
}
```

- [ ] **Step 3: Run E2E test**

```bash
cd task-service && DOCKER_HOST=unix:///run/user/1000/podman/podman.sock go test ./test/e2e/... -run TestE2E_BulkUpdateStatus -v -timeout 120s 2>&1 | tail -20
```

Expected: `TestE2E_BulkUpdateStatus` PASS.

- [ ] **Step 4: Run all E2E tests**

```bash
cd task-service && DOCKER_HOST=unix:///run/user/1000/podman/podman.sock go test ./test/e2e/... -v -timeout 300s 2>&1 | tail -30
```

Expected: all E2E tests PASS.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/tasks_test.go
git commit -m "test(task-service): update E2E test for operator-driven BulkUpdateStatus"
```

---

### Task 7: Update main.go and final build check

**Files:**
- Modify: `task-service/cmd/task-service/main.go`

- [ ] **Step 1: Update main.go wiring**

In `task-service/cmd/task-service/main.go`, replace:
```go
	bulkTimeout := command.NewBulkTimeoutCommand(repo, hub)
```
with:
```go
	bulkUpdateStatus := command.NewBulkUpdateStatusCommand(repo, hub)
```

And replace:
```go
	srv := httpAdapter.NewServer(
		createTask, updateStatus, deleteTask, bulkTimeout,
		getTask, listTasks, taskLineage, handlerStats,
	)
```
with:
```go
	srv := httpAdapter.NewServer(
		createTask, updateStatus, deleteTask, bulkUpdateStatus,
		getTask, listTasks, taskLineage, handlerStats,
	)
```

- [ ] **Step 2: Full build check**

```bash
cd task-service && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run all unit + integration tests**

```bash
cd task-service && go test ./internal/... -v 2>&1 | tail -30
```

Expected: all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/task-service/main.go
git commit -m "feat(task-service): wire BulkUpdateStatusCommand in main"
```

---

### Task 8: Update PR

- [ ] **Step 1: Push branch**

```bash
git push origin feature/phase3-task-service
```

- [ ] **Step 2: Update PR description**

```bash
gh pr edit 3 --body "$(cat <<'EOF'
## Summary

- Replace age-based `BulkTimeout` sweep with operator-driven `BulkUpdateStatus`
- `PATCH /tasks/bulk/status` now accepts `{ids: [string], status: TaskStatus}` — matching spec section 7.6
- Best-effort: non-existent IDs silently skipped, response contains only IDs actually updated
- Fixes ignored `status` field and hardcoded `Timeout` status bugs

## What changed

| Layer | Change |
|-------|--------|
| OpenAPI spec | `BulkTimeoutRequest` fields: `older_than_seconds` → `ids[]` |
| Domain port | `BulkTimeout(olderThanSeconds)` → `BulkUpdateStatus(ids, status)` |
| Postgres | Age-sweep SQL → `UPDATE ... WHERE id IN (ids) RETURNING id` |
| Command | `BulkTimeoutCommand` → `BulkUpdateStatusCommand` |
| HTTP handler | Reads `ids` + `status`; passes to command |

## Test plan

- [x] Unit: `BulkUpdateStatusCommand` publishes SSE for each affected ID
- [x] Integration: updates only matching IDs, skips missing IDs, sets `completed_at` for terminal status
- [x] Handler: `PATCH /tasks/bulk/status` returns 200 with `affected_ids`
- [x] E2E: operator selects specific IDs; unselected task remains `Processing`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Expected: PR #3 description updated.
