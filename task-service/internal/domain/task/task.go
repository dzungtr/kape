package task

import (
	"fmt"
	"time"
)

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

func (s TaskStatus) IsTerminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusSchemaValidationFailed,
		StatusActionError, StatusUnprocessableEvent, StatusTimeout, StatusRetried:
		return true
	}
	return false
}

// EventRaw is the full CloudEvents envelope — immutable after Task creation.
type EventRaw map[string]interface{}

// SchemaOutput is the validated LLM structured output.
type SchemaOutput map[string]interface{}

// ActionResult records outcome of one ActionsRouter action.
type ActionResult struct {
	Name   string  `json:"name"`
	Type   string  `json:"type"`
	Status string  `json:"status"`
	DryRun bool    `json:"dry_run"`
	Error  *string `json:"error"`
}

// Actions is the list of ActionResult objects from the ActionsRouter.
type Actions []ActionResult

// TaskError is present on any non-Completed terminal status.
type TaskError struct {
	Type      string  `json:"type"`
	Detail    string  `json:"detail"`
	Schema    *string `json:"schema"`
	Raw       *string `json:"raw"`
	Traceback *string `json:"traceback"`
}

// Task is the core domain entity.
type Task struct {
	ID           string
	Cluster      string
	Handler      string
	Namespace    string
	EventID      string
	EventSource  string
	EventType    string
	EventRaw     EventRaw
	Status       TaskStatus
	DryRun       bool
	SchemaOutput *SchemaOutput
	Actions      *Actions
	Error        *TaskError
	RetryOf      *string
	OtelTraceID  *string
	ReceivedAt   time.Time
	CompletedAt  *time.Time
	DurationMs   *int
}

// CreateParams holds inputs for NewTask.
type CreateParams struct {
	ID          string
	Cluster     string
	Handler     string
	Namespace   string
	EventID     string
	EventSource string
	EventType   string
	EventRaw    EventRaw
	DryRun      bool
	RetryOf     *string
	OtelTraceID *string
	ReceivedAt  time.Time
}

// NewTask constructs a Task in Processing status.
func NewTask(p CreateParams) *Task {
	return &Task{
		ID:          p.ID,
		Cluster:     p.Cluster,
		Handler:     p.Handler,
		Namespace:   p.Namespace,
		EventID:     p.EventID,
		EventSource: p.EventSource,
		EventType:   p.EventType,
		EventRaw:    p.EventRaw,
		Status:      StatusProcessing,
		DryRun:      p.DryRun,
		RetryOf:     p.RetryOf,
		OtelTraceID: p.OtelTraceID,
		ReceivedAt:  p.ReceivedAt,
	}
}

// ListFilter parameters for Repository.List.
type ListFilter struct {
	Handler string
	Status  TaskStatus
	Since   time.Time
	Sort    string // "received_at:asc" | "received_at:desc"
	Limit   int
	Cursor  string
}

// ErrNotFound is returned by Repository when a Task does not exist.
var ErrNotFound = fmt.Errorf("task not found")

// ErrInvalidTransition is returned when a status transition is not allowed.
var ErrInvalidTransition = fmt.Errorf("invalid status transition")
