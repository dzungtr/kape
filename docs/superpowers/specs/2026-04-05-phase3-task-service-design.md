# Phase 3 — Task-Service Design

**Status:** Approved
**Author:** Dzung Tran
**Created:** 2026-04-05
**Depends on:** specs/0008-audit-db, specs/0009-dashboard-ui, specs/0004-kape-handler, specs/0012-v1-roadmap

---

## Overview

`kape-task-service` is the persistence and streaming API for KAPE Task records. It is the exclusive mediator between handler pods (writers) and the dashboard (reader). No other component connects to PostgreSQL directly.

**Approach:** API-first. The OpenAPI spec (`openapi/openapi.yaml`) is the source of truth. `openapi-generator-cli` with the `go-chi-server` template generates the Chi router and model types. The rest of the service is structured as Domain-Driven Design with dependency inversion (hexagonal/ports-and-adapters).

---

## API Surface

All endpoints defined in `openapi/openapi.yaml`. The spec drives code generation — the YAML is never derived from code.

| Method  | Path                   | Purpose                                      |
|---------|------------------------|----------------------------------------------|
| `POST`  | `/tasks`               | Create task record                           |
| `GET`   | `/tasks`               | List tasks (handler, status, since, sort, limit, cursor filters) |
| `GET`   | `/tasks/stream`        | SSE stream of new/updated task events        |
| `GET`   | `/tasks/decisions`     | Decision distribution aggregates per handler |
| `PATCH` | `/tasks/bulk/status`   | Bulk timeout (mark N Processing tasks)       |
| `GET`   | `/tasks/{id}`          | Fetch single task                            |
| `PATCH` | `/tasks/{id}/status`   | Update task status (single)                  |
| `DELETE`| `/tasks/{id}`          | Discard stale event (no terminal record)     |
| `POST`  | `/tasks/{id}/retry`    | Retry stub — re-publishes event_raw to NATS (Phase 7) |
| `GET`   | `/tasks/{id}/lineage`  | Full retry lineage chain                     |
| `GET`   | `/handlers`            | Per-handler aggregates                       |

---

## Architecture

### Dependency Flow

```
interfaces/http/server.go
        │ calls
        ▼
application/{command,query}
        │ depends on (interfaces only)
        ▼
domain/task/{repository.go, stream.go}   ← ports (interfaces)
        ▲
        │ implements
infrastructure/{postgres, sse}
```

`main.go` is the sole composition root — the only file that knows all layers. It instantiates infrastructure, injects into application, injects into HTTP server.

Generated code under `internal/interfaces/http/gen/` is never edited. The OpenAPI spec is the only place to change the API surface; after changes, regenerate and fix any broken implementations.

### Directory Layout

```
task-service/
├── cmd/task-service/
│   └── main.go                            # composition root: config, DB, migrations, router, server
├── openapi/
│   └── openapi.yaml                       # API source of truth — written first
├── Makefile                               # make generate (openapi-generator), make migrate
├── migrations/
│   ├── 001_create_enum.sql
│   ├── 002_create_tasks.sql
│   └── 003_create_indexes.sql
└── internal/
    ├── interfaces/http/
    │   ├── gen/                           # openapi-generator-cli output — never hand-edited
    │   │   ├── model_*.go                 # request/response structs from OpenAPI schemas
    │   │   ├── api_*.go                   # TasksApiRouter interface (one method per endpoint)
    │   │   └── routers.go                 # Chi route registration
    │   └── server.go                      # implements generated TasksApiRouter interface
    ├── application/
    │   ├── command/
    │   │   ├── create_task.go
    │   │   ├── update_status.go
    │   │   ├── delete_task.go
    │   │   └── bulk_timeout.go
    │   └── query/
    │       ├── get_task.go
    │       ├── list_tasks.go
    │       ├── task_lineage.go
    │       └── handler_stats.go
    ├── domain/task/
    │   ├── task.go                        # Task entity, TaskStatus enum, JSONB value objects
    │   ├── repository.go                  # Repository port (interface)
    │   └── stream.go                      # Stream port (interface)
    └── infrastructure/
        ├── postgres/
        │   ├── task_repository.go         # go-pg implementation of domain.Repository
        │   └── migrate.go                 # golang-migrate runner, embedded SQL files
        └── sse/
            └── hub.go                     # SSE fan-out, implements domain.Stream
```

---

## Domain Layer

### Task Entity (`domain/task/task.go`)

All fields from spec 0008 `tasks` table. JSONB columns are typed Go structs, not `map[string]interface{}`.

```go
type TaskStatus string

const (
    StatusProcessing             TaskStatus = "Processing"
    StatusCompleted              TaskStatus = "Completed"
    StatusFailed                 TaskStatus = "Failed"
    StatusSchemaValidationFailed TaskStatus = "SchemaValidationFailed"
    StatusActionError            TaskStatus = "ActionError"
    StatusUnprocessableEvent     TaskStatus = "UnprocessableEvent"
    StatusPendingApproval        TaskStatus = "PendingApproval"
    StatusTimeout                TaskStatus = "Timeout"
    StatusRetried                TaskStatus = "Retried"
)

type Task struct {
    ID          string        // ULID
    Cluster     string
    Handler     string
    Namespace   string
    EventID     string
    EventSource string
    EventType   string
    EventRaw    EventRaw      // JSONB — full CloudEvents envelope, immutable
    Status      TaskStatus
    DryRun      bool
    SchemaOutput *SchemaOutput // JSONB — nullable until agent completes
    Actions      *Actions      // JSONB — nullable until route_actions runs
    Error        *TaskError    // JSONB — nullable on success
    RetryOf      *string       // FK to original task ID
    OtelTraceID  *string
    ReceivedAt   time.Time
    CompletedAt  *time.Time
    DurationMs   *int
}
```

### Supporting Value Objects (`domain/task/task.go`)

```go
// JSONB value objects — typed, not map[string]interface{}
type EventRaw    map[string]interface{} // full CloudEvents envelope
type SchemaOutput map[string]interface{} // validated LLM structured output

type ActionResult struct {
    Name   string  `json:"name"`
    Type   string  `json:"type"`
    Status string  `json:"status"`
    DryRun bool    `json:"dry_run"`
    Error  *string `json:"error"`
}
type Actions []ActionResult

type TaskError struct {
    Type      string  `json:"type"`      // SchemaValidationFailed | UnhandledError | MalformedEvent | MaxIterationsExceeded
    Detail    string  `json:"detail"`
    Schema    *string `json:"schema"`
    Raw       *string `json:"raw"`
    Traceback *string `json:"traceback"`
}

// ListFilter used by Repository.List and application query
type ListFilter struct {
    Handler  string
    Status   TaskStatus
    Since    time.Time
    Sort     string // "received_at:asc" | "received_at:desc"
    Limit    int
    Cursor   string // ULID cursor for keyset pagination
}

// HandlerStat used by Repository.HandlerStats and application query
type HandlerStat struct {
    Handler         string
    EventCount      int
    StatusBreakdown map[TaskStatus]int
    P99LatencyMs    int
}
```

### Ports

```go
// domain/task/repository.go
type Repository interface {
    Create(ctx context.Context, t *Task) error
    UpdateStatus(ctx context.Context, id string, status TaskStatus, completedAt *time.Time) error
    Delete(ctx context.Context, id string) error
    FindByID(ctx context.Context, id string) (*Task, error)
    List(ctx context.Context, f ListFilter) ([]*Task, int, error)
    Lineage(ctx context.Context, id string) ([]*Task, error)
    HandlerStats(ctx context.Context, since time.Time) ([]HandlerStat, error)
    BulkTimeout(ctx context.Context, olderThanSeconds int) ([]string, error)
}

// domain/task/stream.go
type Stream interface {
    Publish(t *Task)
    Subscribe() (<-chan *Task, func()) // channel + unsubscribe func
}
```

---

## Application Layer

Each command and query is a struct that receives its dependencies by constructor injection. No global state.

**Command pattern:**
```go
type CreateTaskCommand struct {
    repo   task.Repository
    stream task.Stream
}

func NewCreateTaskCommand(repo task.Repository, stream task.Stream) *CreateTaskCommand { ... }

func (c *CreateTaskCommand) Execute(ctx context.Context, input CreateTaskInput) (*task.Task, error) { ... }
```

**Commands:** `CreateTask`, `UpdateStatus`, `DeleteTask`, `BulkTimeout`
**Queries:** `GetTask`, `ListTasks`, `TaskLineage`, `HandlerStats`

Queries receive only `task.Repository` (no stream needed for reads).

---

## Infrastructure Layer

### `postgres.TaskRepository`

- Uses `go-pg/pg/v10` (`*pg.DB`)
- JSONB columns mapped via go-pg struct tags: `pg:"schema_output,type:jsonb"`
- `EnsurePartition(ctx, month time.Time)` called at startup and by a monthly CronJob to pre-create the next partition
- No raw SQL strings outside of migration files — all queries via go-pg query builder

### `sse.Hub`

- Implements `domain.Stream`
- `sync.RWMutex`-guarded subscriber map (subscriber ID → buffered `chan *Task`)
- `Publish`: fan-out to all subscribers; drops message if subscriber channel is full (slow client)
- `Subscribe`: registers channel, returns channel + cleanup func that removes from map

### Migrations

- `golang-migrate/migrate` with `embed.FS` for SQL files
- Run automatically at startup before the HTTP server starts
- Three files: enum creation, table + partition declarations, indexes

---

## HTTP Adapter (`interfaces/http/server.go`)

Implements the interface generated by `openapi-generator-cli -g go-chi-server`.

- Each method: decode request → call application command/query → encode response
- `GET /tasks/stream`: calls `hub.Subscribe()`, writes `data: <json>\n\n` lines until client disconnects or context is cancelled
- `POST /tasks/{id}/retry`: returns `501 Not Implemented` in Phase 3; wired in Phase 7

---

## Database Schema

Executed via embedded migrations. Key points from spec 0008:

- `tasks` partitioned by `RANGE (received_at)` — monthly partitions
- `id` is a ULID (TEXT PRIMARY KEY) — time-sortable, generated by handler runtime
- `event_raw` is JSONB, NOT NULL, immutable after insert
- Terminal states (`Completed`, `Failed`, etc.) are never updated — enforced in application layer
- Four indexes: `received_at DESC`, `(handler, received_at DESC)`, `(status, received_at DESC)`, `retry_of WHERE NOT NULL`

---

## Makefile Targets

```makefile
generate:
    openapi-generator-cli generate \
        -i openapi/openapi.yaml \
        -g go-chi-server \
        -o internal/interfaces/http/gen \
        --additional-properties=packageName=gen

migrate:
    go run ./cmd/task-service --migrate-only
```

---

## Acceptance Criteria (from spec 0012 Phase 3)

- `POST /tasks` creates a Task; `GET /tasks/{id}` returns it
- `PATCH /tasks/{id}/status` transitions `Processing → Completed`; invalid transitions rejected (e.g. `Completed → Processing`)
- `GET /tasks/stream` delivers SSE events when tasks are created or updated
- `GET /handlers` returns correct aggregates for test data
