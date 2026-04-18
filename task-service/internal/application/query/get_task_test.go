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

func TestGetTaskQuery_Found(t *testing.T) {
	repo := mocks.NewRepository(t)
	expected := &task.Task{ID: "01GET"}
	repo.On("FindByID", mock.Anything, "01GET").Return(expected, nil)

	q := query.NewGetTaskQuery(repo)
	result, err := q.Execute(context.Background(), "01GET")
	require.NoError(t, err)
	assert.Equal(t, "01GET", result.ID)
}

func TestGetTaskQuery_NotFound(t *testing.T) {
	repo := mocks.NewRepository(t)
	repo.On("FindByID", mock.Anything, "GHOST").Return(nil, task.ErrNotFound)

	q := query.NewGetTaskQuery(repo)
	_, err := q.Execute(context.Background(), "GHOST")
	assert.ErrorIs(t, err, task.ErrNotFound)
}
