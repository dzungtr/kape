package command_test

import (
	"context"
	"testing"

	"github.com/kape-io/kape/task-service/internal/application/command"
	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/kape-io/kape/task-service/internal/domain/task/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func processingTask(id string) *task.Task {
	return &task.Task{ID: id, Status: task.StatusProcessing}
}

func TestUpdateStatusCommand_ValidTransition_ProcessingToCompleted(t *testing.T) {
	repo := mocks.NewRepository(t)
	stream := mocks.NewStream(t)
	updated := &task.Task{ID: "01T", Status: task.StatusCompleted}

	repo.On("FindByID", mock.Anything, "01T").Return(processingTask("01T"), nil).Once()
	repo.On("UpdateStatus", mock.Anything, "01T", task.StatusCompleted, mock.Anything).Return(nil)
	repo.On("FindByID", mock.Anything, "01T").Return(updated, nil).Once()
	stream.On("Publish", updated).Return()

	cmd := command.NewUpdateStatusCommand(repo, stream)
	result, err := cmd.Execute(context.Background(), command.UpdateStatusInput{ID: "01T", Status: task.StatusCompleted})
	require.NoError(t, err)
	assert.Equal(t, task.StatusCompleted, result.Status)
}

func TestUpdateStatusCommand_InvalidTransition_CompletedToProcessing(t *testing.T) {
	repo := mocks.NewRepository(t)
	stream := mocks.NewStream(t)

	repo.On("FindByID", mock.Anything, "01T").Return(&task.Task{ID: "01T", Status: task.StatusCompleted}, nil)

	cmd := command.NewUpdateStatusCommand(repo, stream)
	_, err := cmd.Execute(context.Background(), command.UpdateStatusInput{ID: "01T", Status: task.StatusProcessing})
	require.Error(t, err)
	assert.ErrorIs(t, err, task.ErrInvalidTransition)
	stream.AssertNotCalled(t, "Publish", mock.Anything)
}

func TestUpdateStatusCommand_ValidTransitions_AllFromProcessing(t *testing.T) {
	targets := []task.TaskStatus{
		task.StatusCompleted, task.StatusFailed, task.StatusSchemaValidationFailed,
		task.StatusActionError, task.StatusUnprocessableEvent, task.StatusTimeout, task.StatusRetried,
	}
	for _, target := range targets {
		t.Run(string(target), func(t *testing.T) {
			repo := mocks.NewRepository(t)
			stream := mocks.NewStream(t)
			result := &task.Task{ID: "01T", Status: target}

			repo.On("FindByID", mock.Anything, "01T").Return(processingTask("01T"), nil).Once()
			repo.On("UpdateStatus", mock.Anything, "01T", target, mock.Anything).Return(nil)
			repo.On("FindByID", mock.Anything, "01T").Return(result, nil).Once()
			stream.On("Publish", mock.Anything).Return()

			cmd := command.NewUpdateStatusCommand(repo, stream)
			_, err := cmd.Execute(context.Background(), command.UpdateStatusInput{ID: "01T", Status: target})
			require.NoError(t, err)
		})
	}
}
