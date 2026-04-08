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
	HandlerStats(ctx context.Context, since time.Time) ([]HandlerStat, error)
	BulkUpdateStatus(ctx context.Context, ids []string, status TaskStatus) ([]string, error)
}

// UpdateFields carries optional columns to update alongside status.
type UpdateFields struct {
	CompletedAt  *time.Time
	SchemaOutput *SchemaOutput
	Actions      *Actions
	Error        *TaskError
	DurationMs   *int
}
