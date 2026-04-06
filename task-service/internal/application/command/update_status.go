package command

import (
	"context"
	"fmt"

	"github.com/kape-io/kape/task-service/internal/domain/task"
)

// validTransitions defines allowed status transitions.
// Key: current status; Value: set of allowed next statuses.
var validTransitions = map[task.TaskStatus]map[task.TaskStatus]bool{
	task.StatusProcessing: {
		task.StatusCompleted:              true,
		task.StatusFailed:                 true,
		task.StatusSchemaValidationFailed: true,
		task.StatusActionError:            true,
		task.StatusUnprocessableEvent:     true,
		task.StatusTimeout:                true,
		task.StatusRetried:                true,
	},
}

type UpdateStatusInput struct {
	ID     string
	Status task.TaskStatus
	Fields task.UpdateFields
}

type UpdateStatusCommand struct {
	repo   task.Repository
	stream task.Stream
}

func NewUpdateStatusCommand(repo task.Repository, stream task.Stream) *UpdateStatusCommand {
	return &UpdateStatusCommand{repo: repo, stream: stream}
}

func (c *UpdateStatusCommand) Execute(ctx context.Context, in UpdateStatusInput) (*task.Task, error) {
	current, err := c.repo.FindByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	allowed, ok := validTransitions[current.Status]
	if !ok || !allowed[in.Status] {
		return nil, fmt.Errorf("%w: %s → %s", task.ErrInvalidTransition, current.Status, in.Status)
	}

	if err := c.repo.UpdateStatus(ctx, in.ID, in.Status, in.Fields); err != nil {
		return nil, err
	}

	updated, err := c.repo.FindByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	c.stream.Publish(updated)
	return updated, nil
}
