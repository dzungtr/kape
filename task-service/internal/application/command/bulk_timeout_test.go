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

func TestBulkTimeoutCommand_PublishesForEachAffected(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	stream := mocks.NewMockStream(t)

	repo.On("BulkTimeout", mock.Anything, 3600).Return([]string{"01A", "01B"}, nil)
	stream.On("Publish", mock.MatchedBy(func(tsk *task.Task) bool {
		return tsk.Status == task.StatusTimeout
	})).Return().Times(2)

	cmd := command.NewBulkTimeoutCommand(repo, stream)
	ids, err := cmd.Execute(context.Background(), command.BulkTimeoutInput{OlderThanSeconds: 3600})
	require.NoError(t, err)
	assert.Equal(t, []string{"01A", "01B"}, ids)
	stream.AssertNumberOfCalls(t, "Publish", 2)
}

func TestBulkTimeoutCommand_NoneAffected_NoPublish(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	stream := mocks.NewMockStream(t)

	repo.On("BulkTimeout", mock.Anything, 3600).Return([]string{}, nil)

	cmd := command.NewBulkTimeoutCommand(repo, stream)
	ids, err := cmd.Execute(context.Background(), command.BulkTimeoutInput{OlderThanSeconds: 3600})
	require.NoError(t, err)
	assert.Empty(t, ids)
	stream.AssertNotCalled(t, "Publish", mock.Anything)
}
