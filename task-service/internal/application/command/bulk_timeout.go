package command

import (
	"context"

	"github.com/kape-io/kape/task-service/internal/domain/task"
)

type BulkTimeoutInput struct {
	OlderThanSeconds int
}

type BulkTimeoutCommand struct {
	repo   task.Repository
	stream task.Stream
}

func NewBulkTimeoutCommand(repo task.Repository, stream task.Stream) *BulkTimeoutCommand {
	return &BulkTimeoutCommand{repo: repo, stream: stream}
}

func (c *BulkTimeoutCommand) Execute(ctx context.Context, in BulkTimeoutInput) ([]string, error) {
	ids, err := c.repo.BulkTimeout(ctx, in.OlderThanSeconds)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		c.stream.Publish(&task.Task{ID: id, Status: task.StatusTimeout})
	}
	return ids, nil
}
