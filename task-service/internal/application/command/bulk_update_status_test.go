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

func TestBulkUpdateStatusCommand_PublishesForEachAffected(t *testing.T) {
	repo := mocks.NewRepository(t)
	stream := mocks.NewStream(t)

	repo.On("BulkUpdateStatus", mock.Anything, []string{"01A", "01B"}, task.StatusTimeout).
		Return([]string{"01A", "01B"}, nil)
	stream.On("Publish", mock.MatchedBy(func(tsk *task.Task) bool {
		return tsk.Status == task.StatusTimeout
	})).Return().Times(2)

	cmd := command.NewBulkUpdateStatusCommand(repo, stream)
	ids, err := cmd.Execute(context.Background(), command.BulkUpdateStatusInput{
		IDs:    []string{"01A", "01B"},
		Status: task.StatusTimeout,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"01A", "01B"}, ids)
	stream.AssertNumberOfCalls(t, "Publish", 2)
}

func TestBulkUpdateStatusCommand_NoneAffected_NoPublish(t *testing.T) {
	repo := mocks.NewRepository(t)
	stream := mocks.NewStream(t)

	repo.On("BulkUpdateStatus", mock.Anything, []string{"01X"}, task.StatusTimeout).
		Return([]string{}, nil)

	cmd := command.NewBulkUpdateStatusCommand(repo, stream)
	ids, err := cmd.Execute(context.Background(), command.BulkUpdateStatusInput{
		IDs:    []string{"01X"},
		Status: task.StatusTimeout,
	})
	require.NoError(t, err)
	assert.Empty(t, ids)
	stream.AssertNotCalled(t, "Publish", mock.Anything)
}
