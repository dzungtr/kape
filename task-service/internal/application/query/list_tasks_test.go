package query_test

import (
	"context"
	"testing"

	"github.com/kape-io/kape/task-service/internal/application/query"
	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/kape-io/kape/task-service/internal/domain/task/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestListTasksQuery_ReturnsCursor_WhenPageFull(t *testing.T) {
	repo := mocks.NewRepository(t)
	tasks := []*task.Task{{ID: "01A"}, {ID: "01B"}}
	repo.On("List", mock.Anything, task.ListFilter{Limit: 2}).Return(tasks, 5, nil)

	q := query.NewListTasksQuery(repo)
	result, err := q.Execute(context.Background(), task.ListFilter{Limit: 2})
	require.NoError(t, err)
	require.NotNil(t, result.NextCursor)
	assert.Equal(t, "01B", *result.NextCursor)
	assert.Equal(t, 5, result.Total)
}

func TestListTasksQuery_NoCursor_WhenPagePartial(t *testing.T) {
	repo := mocks.NewRepository(t)
	tasks := []*task.Task{{ID: "01A"}}
	repo.On("List", mock.Anything, task.ListFilter{Limit: 10}).Return(tasks, 1, nil)

	q := query.NewListTasksQuery(repo)
	result, err := q.Execute(context.Background(), task.ListFilter{Limit: 10})
	require.NoError(t, err)
	assert.Nil(t, result.NextCursor)
}
