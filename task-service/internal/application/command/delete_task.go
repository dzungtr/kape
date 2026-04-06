package command

import (
	"context"

	"github.com/kape-io/kape/task-service/internal/domain/task"
)

type DeleteTaskCommand struct {
	repo task.Repository
}

func NewDeleteTaskCommand(repo task.Repository) *DeleteTaskCommand {
	return &DeleteTaskCommand{repo: repo}
}

func (c *DeleteTaskCommand) Execute(ctx context.Context, id string) error {
	return c.repo.Delete(ctx, id)
}
