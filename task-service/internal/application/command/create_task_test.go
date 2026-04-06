package command_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kape-io/kape/task-service/internal/application/command"
	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/kape-io/kape/task-service/internal/domain/task/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCreateTaskCommand_Execute_CreatesAndPublishes(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	stream := mocks.NewMockStream(t)

	repo.On("Create", mock.Anything, mock.MatchedBy(func(tsk *task.Task) bool {
		return tsk.ID == "01TEST" && tsk.Status == task.StatusProcessing
	})).Return(nil)
	stream.On("Publish", mock.MatchedBy(func(tsk *task.Task) bool {
		return tsk.ID == "01TEST"
	})).Return()

	cmd := command.NewCreateTaskCommand(repo, stream)
	result, err := cmd.Execute(context.Background(), command.CreateTaskInput{
		Params: task.CreateParams{
			ID: "01TEST", Cluster: "c", Handler: "h", Namespace: "ns",
			EventID: "e", EventSource: "s", EventType: "t",
			EventRaw: task.EventRaw{"specversion": "1.0"},
			DryRun: false, ReceivedAt: time.Now(),
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "01TEST", result.ID)
	assert.Equal(t, task.StatusProcessing, result.Status)
	repo.AssertExpectations(t)
	stream.AssertExpectations(t)
}

func TestCreateTaskCommand_Execute_RepoErrorDoesNotPublish(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	stream := mocks.NewMockStream(t)

	repo.On("Create", mock.Anything, mock.Anything).Return(fmt.Errorf("db error"))
	// stream.Publish must NOT be called

	cmd := command.NewCreateTaskCommand(repo, stream)
	_, err := cmd.Execute(context.Background(), command.CreateTaskInput{
		Params: task.CreateParams{ID: "01FAIL", ReceivedAt: time.Now()},
	})
	require.Error(t, err)
	stream.AssertNotCalled(t, "Publish", mock.Anything)
}
