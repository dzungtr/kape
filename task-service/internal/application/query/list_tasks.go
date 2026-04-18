package query

import (
	"context"

	"github.com/kape-io/kape/task-service/internal/domain/task"
)

type ListTasksResult struct {
	Tasks      []*task.Task
	Total      int
	NextCursor *string
}

type ListTasksQuery struct{ repo task.Repository }

func NewListTasksQuery(repo task.Repository) *ListTasksQuery { return &ListTasksQuery{repo: repo} }

func (q *ListTasksQuery) Execute(ctx context.Context, f task.ListFilter) (*ListTasksResult, error) {
	tasks, total, err := q.repo.List(ctx, f)
	if err != nil {
		return nil, err
	}

	var nextCursor *string
	if len(tasks) == f.Limit && f.Limit > 0 {
		last := tasks[len(tasks)-1].ID
		nextCursor = &last
	}

	return &ListTasksResult{Tasks: tasks, Total: total, NextCursor: nextCursor}, nil
}
