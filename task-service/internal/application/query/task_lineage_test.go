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

func TestTaskLineageQuery_ReturnsChain(t *testing.T) {
	repo := mocks.NewRepository(t)
	chain := []*task.Task{{ID: "01ROOT"}, {ID: "01RETRY"}}
	repo.On("Lineage", mock.Anything, "01RETRY").Return(chain, nil)

	q := query.NewTaskLineageQuery(repo)
	result, err := q.Execute(context.Background(), "01RETRY")
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "01ROOT", result[0].ID)
}
