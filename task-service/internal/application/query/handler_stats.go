package query

import (
	"context"
	"time"

	"github.com/kape-io/kape/task-service/internal/domain/task"
)

type HandlerStatsQuery struct{ repo task.Repository }

func NewHandlerStatsQuery(repo task.Repository) *HandlerStatsQuery {
	return &HandlerStatsQuery{repo: repo}
}

func (q *HandlerStatsQuery) Execute(ctx context.Context, since time.Time) ([]task.HandlerStat, error) {
	return q.repo.HandlerStats(ctx, since)
}
