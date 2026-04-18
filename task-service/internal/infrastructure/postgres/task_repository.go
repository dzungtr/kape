package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-pg/pg/v10"
	"github.com/kape-io/kape/task-service/internal/domain/task"
)

// taskRow is the go-pg model. It mirrors the tasks table exactly.
// JSONB columns are typed Go structs — go-pg marshals/unmarshals them automatically.
type taskRow struct {
	tableName struct{} `pg:"tasks,discard_unknown_columns"`

	ID           string             `pg:"id,pk"`
	Cluster      string             `pg:"cluster,notnull"`
	Handler      string             `pg:"handler,notnull"`
	Namespace    string             `pg:"namespace,notnull"`
	EventID      string             `pg:"event_id,notnull"`
	EventSource  string             `pg:"event_source,notnull"`
	EventType    string             `pg:"event_type,notnull"`
	EventRaw     task.EventRaw      `pg:"event_raw,type:jsonb,notnull"`
	Status       task.TaskStatus    `pg:"status,notnull"`
	DryRun       bool               `pg:"dry_run,notnull"`
	SchemaOutput *task.SchemaOutput `pg:"schema_output,type:jsonb"`
	Actions      *task.Actions      `pg:"actions,type:jsonb"`
	Error        *task.TaskError    `pg:"error,type:jsonb"`
	RetryOf      *string            `pg:"retry_of"`
	OtelTraceID  *string            `pg:"otel_trace_id"`
	ReceivedAt   time.Time          `pg:"received_at,notnull"`
	CompletedAt  *time.Time         `pg:"completed_at"`
	DurationMs   *int               `pg:"duration_ms"`
}

func toRow(t *task.Task) *taskRow {
	return &taskRow{
		ID: t.ID, Cluster: t.Cluster, Handler: t.Handler, Namespace: t.Namespace,
		EventID: t.EventID, EventSource: t.EventSource, EventType: t.EventType,
		EventRaw: t.EventRaw, Status: t.Status, DryRun: t.DryRun,
		SchemaOutput: t.SchemaOutput, Actions: t.Actions, Error: t.Error,
		RetryOf: t.RetryOf, OtelTraceID: t.OtelTraceID,
		ReceivedAt: t.ReceivedAt, CompletedAt: t.CompletedAt, DurationMs: t.DurationMs,
	}
}

func fromRow(r *taskRow) *task.Task {
	return &task.Task{
		ID: r.ID, Cluster: r.Cluster, Handler: r.Handler, Namespace: r.Namespace,
		EventID: r.EventID, EventSource: r.EventSource, EventType: r.EventType,
		EventRaw: r.EventRaw, Status: r.Status, DryRun: r.DryRun,
		SchemaOutput: r.SchemaOutput, Actions: r.Actions, Error: r.Error,
		RetryOf: r.RetryOf, OtelTraceID: r.OtelTraceID,
		ReceivedAt: r.ReceivedAt, CompletedAt: r.CompletedAt, DurationMs: r.DurationMs,
	}
}

// TaskRepository implements domain.Repository using go-pg.
type TaskRepository struct {
	db *pg.DB
}

func NewTaskRepository(db *pg.DB) *TaskRepository {
	return &TaskRepository{db: db}
}

func (r *TaskRepository) Create(ctx context.Context, t *task.Task) error {
	row := toRow(t)
	_, err := r.db.ModelContext(ctx, row).Insert()
	return err
}

func (r *TaskRepository) UpdateStatus(ctx context.Context, id string, status task.TaskStatus, fields task.UpdateFields) error {
	row := &taskRow{
		ID:           id,
		Status:       status,
		CompletedAt:  fields.CompletedAt,
		SchemaOutput: fields.SchemaOutput,
		Actions:      fields.Actions,
		Error:        fields.Error,
		DurationMs:   fields.DurationMs,
	}
	cols := []string{"status"}
	if fields.CompletedAt != nil {
		cols = append(cols, "completed_at")
	}
	if fields.SchemaOutput != nil {
		cols = append(cols, "schema_output")
	}
	if fields.Actions != nil {
		cols = append(cols, "actions")
	}
	if fields.Error != nil {
		cols = append(cols, "error")
	}
	if fields.DurationMs != nil {
		cols = append(cols, "duration_ms")
	}
	res, err := r.db.ModelContext(ctx, row).
		Column(cols...).
		Where("id = ?", id).
		Update()
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return task.ErrNotFound
	}
	return nil
}

func (r *TaskRepository) Delete(ctx context.Context, id string) error {
	row := &taskRow{}
	res, err := r.db.ModelContext(ctx, row).Where("id = ?", id).Delete()
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return task.ErrNotFound
	}
	return nil
}

func (r *TaskRepository) FindByID(ctx context.Context, id string) (*task.Task, error) {
	row := &taskRow{}
	err := r.db.ModelContext(ctx, row).Where("id = ?", id).Select()
	if errors.Is(err, pg.ErrNoRows) {
		return nil, task.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return fromRow(row), nil
}

func (r *TaskRepository) List(ctx context.Context, f task.ListFilter) ([]*task.Task, int, error) {
	var rows []taskRow
	q := r.db.ModelContext(ctx, &rows)

	if f.Handler != "" {
		q = q.Where("handler = ?", f.Handler)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if !f.Since.IsZero() {
		q = q.Where("received_at >= ?", f.Since)
	}
	if f.Cursor != "" {
		if f.Sort == "received_at:asc" {
			q = q.Where("id > ?", f.Cursor)
		} else {
			q = q.Where("id < ?", f.Cursor)
		}
	}

	total, err := q.Count()
	if err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	if f.Sort == "received_at:asc" {
		q = q.OrderExpr("received_at ASC")
	} else {
		q = q.OrderExpr("received_at DESC")
	}
	q = q.Limit(limit)

	if err := q.Select(); err != nil {
		return nil, 0, err
	}

	tasks := make([]*task.Task, len(rows))
	for i := range rows {
		tasks[i] = fromRow(&rows[i])
	}
	return tasks, total, nil
}

func (r *TaskRepository) Lineage(ctx context.Context, id string) ([]*task.Task, error) {
	// Walk up to root, then fetch all tasks sharing that root.
	root, err := r.findRoot(ctx, id)
	if err != nil {
		return nil, err
	}

	var rows []taskRow
	// Root task itself
	err = r.db.ModelContext(ctx, &rows).
		Where("id = ? OR retry_of = ?", root, root).
		OrderExpr("received_at ASC").
		Select()
	if err != nil {
		return nil, err
	}

	tasks := make([]*task.Task, len(rows))
	for i := range rows {
		tasks[i] = fromRow(&rows[i])
	}
	return tasks, nil
}

// findRoot walks retry_of links upward to find the original task ID.
func (r *TaskRepository) findRoot(ctx context.Context, id string) (string, error) {
	current := id
	for {
		row := &taskRow{}
		if err := r.db.ModelContext(ctx, row).Column("id", "retry_of").Where("id = ?", current).Select(); err != nil {
			if errors.Is(err, pg.ErrNoRows) {
				return "", task.ErrNotFound
			}
			return "", err
		}
		if row.RetryOf == nil {
			return current, nil
		}
		current = *row.RetryOf
	}
}

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

// EnsurePartition creates the monthly partition for the given month if it doesn't exist.
func (r *TaskRepository) EnsurePartition(ctx context.Context, month time.Time) error {
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	name := fmt.Sprintf("tasks_%04d_%02d", start.Year(), int(start.Month()))
	_, err := r.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s PARTITION OF tasks
		FOR VALUES FROM ('%s') TO ('%s')
	`, name, start.Format("2006-01-02"), end.Format("2006-01-02")))
	return err
}
