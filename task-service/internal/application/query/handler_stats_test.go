package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/kape-io/kape/task-service/internal/application/query"
	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/kape-io/kape/task-service/internal/domain/task/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandlerStatsQuery_DelegatesToRepo(t *testing.T) {
	repo := mocks.NewRepository(t)
	since := time.Now().Add(-time.Hour)
	expected := []task.HandlerStat{{Handler: "falco", EventCount: 10}}
	repo.On("HandlerStats", mock.Anything, since).Return(expected, nil)

	q := query.NewHandlerStatsQuery(repo)
	result, err := q.Execute(context.Background(), since)
	require.NoError(t, err)
	assert.Equal(t, expected, result)
}
