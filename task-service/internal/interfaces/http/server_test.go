package http_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kape-io/kape/task-service/internal/application/command"
	"github.com/kape-io/kape/task-service/internal/application/query"
	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/kape-io/kape/task-service/internal/domain/task/mocks"
	httpAdapter "github.com/kape-io/kape/task-service/internal/interfaces/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func buildServer(t *testing.T) (*httpAdapter.Server, *mocks.Repository, *mocks.Stream) {
	repo := mocks.NewRepository(t)
	stream := mocks.NewStream(t)
	srv := httpAdapter.NewServer(
		command.NewCreateTaskCommand(repo, stream),
		command.NewUpdateStatusCommand(repo, stream),
		command.NewDeleteTaskCommand(repo),
		command.NewBulkUpdateStatusCommand(repo, stream),
		query.NewGetTaskQuery(repo),
		query.NewListTasksQuery(repo),
		query.NewTaskLineageQuery(repo),
		repo,
	)
	return srv, repo, stream
}

func chiRouter(srv *httpAdapter.Server) *chi.Mux {
	r := chi.NewRouter()
	r.Post("/tasks", srv.CreateTask)
	r.Get("/tasks", srv.ListTasks)
	r.Get("/tasks/decisions", srv.GetDecisions)
	r.Patch("/tasks/bulk/status", srv.BulkUpdateStatus)
	r.Get("/tasks/{id}", srv.GetTask)
	r.Patch("/tasks/{id}/status", srv.UpdateTaskStatus)
	r.Delete("/tasks/{id}", srv.DeleteTask)
	r.Post("/tasks/{id}/retry", srv.RetryTask)
	r.Get("/tasks/{id}/lineage", srv.GetTaskLineage)
	r.Get("/handlers", srv.ListHandlers)
	return r
}

func TestServer_CreateTask_201(t *testing.T) {
	srv, repo, stream := buildServer(t)
	now := time.Now().UTC()

	repo.On("Create", mock.Anything, mock.Anything).Return(nil)
	stream.On("Publish", mock.Anything).Return()

	body, _ := json.Marshal(map[string]interface{}{
		"id": "01T", "cluster": "c", "handler": "h", "namespace": "ns",
		"event_id": "e", "event_source": "s", "event_type": "t",
		"event_raw": map[string]interface{}{"specversion": "1.0"},
		"status": "Processing", "dry_run": false, "received_at": now,
	})
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusCreated, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "01T", resp["id"])
}

func TestServer_CreateTask_400_InvalidBody(t *testing.T) {
	srv, _, _ := buildServer(t)
	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader([]byte("not-json")))
	rec := httptest.NewRecorder()
	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestServer_GetTask_200(t *testing.T) {
	srv, repo, _ := buildServer(t)
	repo.On("FindByID", mock.Anything, "01T").Return(&task.Task{
		ID: "01T", Status: task.StatusProcessing,
		EventRaw: task.EventRaw{},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/tasks/01T", nil)
	rec := httptest.NewRecorder()
	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "01T", resp["id"])
}

func TestServer_GetTask_404(t *testing.T) {
	srv, repo, _ := buildServer(t)
	repo.On("FindByID", mock.Anything, "GHOST").Return(nil, task.ErrNotFound)

	req := httptest.NewRequest(http.MethodGet, "/tasks/GHOST", nil)
	rec := httptest.NewRecorder()
	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServer_UpdateTaskStatus_409_InvalidTransition(t *testing.T) {
	srv, repo, _ := buildServer(t)
	repo.On("FindByID", mock.Anything, "01T").Return(&task.Task{ID: "01T", Status: task.StatusCompleted}, nil)

	body, _ := json.Marshal(map[string]interface{}{"status": "Processing"})
	req := httptest.NewRequest(http.MethodPatch, "/tasks/01T/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestServer_DeleteTask_204(t *testing.T) {
	srv, repo, _ := buildServer(t)
	repo.On("Delete", mock.Anything, "01T").Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/tasks/01T", nil)
	rec := httptest.NewRecorder()
	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestServer_RetryTask_501(t *testing.T) {
	srv, _, _ := buildServer(t)
	req := httptest.NewRequest(http.MethodPost, "/tasks/01T/retry", nil)
	rec := httptest.NewRecorder()
	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
}
func TestServer_UpdateTaskStatus_409_TerminalState(t *testing.T) {
	srv, repo, _ := buildServer(t)
	repo.On("FindByID", mock.Anything, "01T").Return(&task.Task{ID: "01T", Status: task.StatusProcessing}, nil)
	repo.On("UpdateStatus", mock.Anything, "01T", mock.Anything, mock.Anything).Return(task.ErrTerminalState)

	body, _ := json.Marshal(map[string]interface{}{"status": "Failed"})
	req := httptest.NewRequest(http.MethodPatch, "/tasks/01T/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusConflict, rec.Code)
}
func TestServer_ListHandlers_200(t *testing.T) {
	srv, repo, _ := buildServer(t)
	now := time.Now().UTC()
	repo.On("ListHandlers", mock.Anything, mock.Anything).Return([]task.HandlerAggregate{
		{Handler: "h1", Namespace: "kape-system", LastTaskAt: &now, Tasks24h: 5, Failures24h: 1, ProcessingCount: 2},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/handlers", nil)
	rec := httptest.NewRecorder()
	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp []map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "h1", resp[0]["handler"])
	assert.Equal(t, "kape-system", resp[0]["namespace"])
	assert.Equal(t, float64(5), resp[0]["tasks_24h"])
	assert.Equal(t, float64(1), resp[0]["failures_24h"])
	assert.Equal(t, float64(2), resp[0]["processing_count"])
}

func TestServer_GetDecisions_200(t *testing.T) {
	srv, repo, _ := buildServer(t)
	since := time.Now().UTC().Add(-24 * time.Hour)
	repo.On("GetDecisionDistribution", mock.Anything, "h1", mock.Anything).Return(&task.DecisionDistribution{
		Handler:      "h1",
		Since:        since,
		Distribution: map[string]int{"allow": 3, "deny": 1},
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/tasks/decisions?handler=h1&since="+since.Format(time.RFC3339), nil)
	rec := httptest.NewRecorder()
	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "h1", resp["handler"])
	dist, ok := resp["distribution"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(3), dist["allow"])
	assert.Equal(t, float64(1), dist["deny"])
}

func TestServer_GetDecisions_400_MissingSince(t *testing.T) {
	srv, _, _ := buildServer(t)
	req := httptest.NewRequest(http.MethodGet, "/tasks/decisions?handler=h1", nil)
	rec := httptest.NewRecorder()
	chiRouter(srv).ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
