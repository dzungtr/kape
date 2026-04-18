package query

import (
	"context"

	"github.com/kape-io/kape/task-service/internal/domain/task"
)

type GetTaskQuery struct{ repo task.Repository }

func NewGetTaskQuery(repo task.Repository) *GetTaskQuery { return &GetTaskQuery{repo: repo} }

func (q *GetTaskQuery) Execute(ctx context.Context, id string) (*task.Task, error) {
	return q.repo.FindByID(ctx, id)
}
