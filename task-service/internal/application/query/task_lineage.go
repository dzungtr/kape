package query

import (
	"context"

	"github.com/kape-io/kape/task-service/internal/domain/task"
)

type TaskLineageQuery struct{ repo task.Repository }

func NewTaskLineageQuery(repo task.Repository) *TaskLineageQuery {
	return &TaskLineageQuery{repo: repo}
}

func (q *TaskLineageQuery) Execute(ctx context.Context, id string) ([]*task.Task, error) {
	return q.repo.Lineage(ctx, id)
}
