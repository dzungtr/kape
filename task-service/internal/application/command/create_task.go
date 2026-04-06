package command

import (
	"context"

	"github.com/kape-io/kape/task-service/internal/domain/task"
)

type CreateTaskInput struct {
	Params task.CreateParams
}

type CreateTaskCommand struct {
	repo   task.Repository
	stream task.Stream
}

func NewCreateTaskCommand(repo task.Repository, stream task.Stream) *CreateTaskCommand {
	return &CreateTaskCommand{repo: repo, stream: stream}
}

func (c *CreateTaskCommand) Execute(ctx context.Context, in CreateTaskInput) (*task.Task, error) {
	t := task.NewTask(in.Params)
	if err := c.repo.Create(ctx, t); err != nil {
		return nil, err
	}
	c.stream.Publish(t)
	return t, nil
}
