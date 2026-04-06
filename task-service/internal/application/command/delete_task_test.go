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

func TestDeleteTaskCommand_Execute_Delegates(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.On("Delete", mock.Anything, "01DEL").Return(nil)

	cmd := command.NewDeleteTaskCommand(repo)
	require.NoError(t, cmd.Execute(context.Background(), "01DEL"))
}

func TestDeleteTaskCommand_Execute_NotFound(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	repo.On("Delete", mock.Anything, "GHOST").Return(task.ErrNotFound)

	cmd := command.NewDeleteTaskCommand(repo)
	err := cmd.Execute(context.Background(), "GHOST")
	assert.ErrorIs(t, err, task.ErrNotFound)
}
