package task

import (
	"context"
	"time"
)

// Repository is the port for Task persistence. Implemented by infrastructure/postgres.
type Repository interface {
	Create(ctx context.Context, t *Task) error
	UpdateStatus(ctx context.Context, id string, status TaskStatus, fields UpdateFields) error
	Delete(ctx context.Context, id string) error
	FindByID(ctx context.Context, id string) (*Task, error)
	List(ctx context.Context, f ListFilter) ([]*Task, int, error)
	Lineage(ctx context.Context, id string) ([]*Task, error)
	BulkUpdateStatus(ctx context.Context, ids []string, status TaskStatus) ([]string, error)
	ListHandlers(ctx context.Context, since time.Time) ([]HandlerAggregate, error)
	GetDecisionDistribution(ctx context.Context, handler string, since time.Time) (*DecisionDistribution, error)
}

// HandlerAggregate is a per-handler summary derived from the tasks table.
type HandlerAggregate struct {
	Handler         string
	Namespace       string
	LastTaskAt      *time.Time
	Tasks24h        int
	Failures24h     int
	ProcessingCount int
}

// DecisionDistribution is the breakdown of LLM decision values for a handler over a time window.
type DecisionDistribution struct {
	Handler      string
	Since        time.Time
	Distribution map[string]int
}

// UpdateFields carries optional columns to update alongside status.
type UpdateFields struct {
	CompletedAt  *time.Time
	SchemaOutput *SchemaOutput
	Actions      *Actions
	Error        *TaskError
	DurationMs   *int
}
