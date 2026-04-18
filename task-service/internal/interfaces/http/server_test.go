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
