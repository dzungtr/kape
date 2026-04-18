package command

import (
	"context"

	"github.com/kape-io/kape/task-service/internal/domain/task"
)

type BulkUpdateStatusInput struct {
	IDs    []string
	Status task.TaskStatus
}

type BulkUpdateStatusCommand struct {
	repo   task.Repository
	stream task.Stream
}

func NewBulkUpdateStatusCommand(repo task.Repository, stream task.Stream) *BulkUpdateStatusCommand {
	return &BulkUpdateStatusCommand{repo: repo, stream: stream}
}

func (c *BulkUpdateStatusCommand) Execute(ctx context.Context, in BulkUpdateStatusInput) ([]string, error) {
	ids, err := c.repo.BulkUpdateStatus(ctx, in.IDs, in.Status)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		c.stream.Publish(&task.Task{ID: id, Status: in.Status})
	}
	return ids, nil
}
