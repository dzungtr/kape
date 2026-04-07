//go:build e2e

// task-service/test/e2e/tasks_test.go
package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	gopg "github.com/go-pg/pg/v10"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/kape-io/kape/task-service/internal/application/command"
	"github.com/kape-io/kape/task-service/internal/application/query"
	"github.com/kape-io/kape/task-service/internal/infrastructure/postgres"
	"github.com/kape-io/kape/task-service/internal/infrastructure/sse"
	httpAdapter "github.com/kape-io/kape/task-service/internal/interfaces/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	testcontainers "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

type testEnv struct {
	baseURL string
	client  *http.Client
}

func startServer(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:16-alpine"),
		tcpostgres.WithDatabase("e2edb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { container.Terminate(ctx) })

	host, _ := container.Host(ctx)
	port, _ := container.MappedPort(ctx, "5432")
	dsn := fmt.Sprintf("postgres://test:test@%s:%s/e2edb?sslmode=disable", host, port.Port())

	require.NoError(t, postgres.RunMigrations(dsn))

	opts, _ := gopg.ParseURL(dsn)
	db := gopg.Connect(opts)
	t.Cleanup(func() { db.Close() })

	repo := postgres.NewTaskRepository(db)
	hub := sse.NewHub()

	srv := httpAdapter.NewServer(
		command.NewCreateTaskCommand(repo, hub),
		command.NewUpdateStatusCommand(repo, hub),
		command.NewDeleteTaskCommand(repo),
		command.NewBulkTimeoutCommand(repo, hub),
		query.NewGetTaskQuery(repo),
		query.NewListTasksQuery(repo),
		query.NewTaskLineageQuery(repo),
		query.NewHandlerStatsQuery(repo),
	)

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Post("/tasks", srv.CreateTask)
	r.Get("/tasks", srv.ListTasks)
	r.Get("/tasks/stream", httpAdapter.NewSSEHandler(hub).ServeHTTP)
	r.Patch("/tasks/bulk/status", srv.BulkUpdateStatus)
	r.Get("/tasks/{id}", srv.GetTask)
	r.Patch("/tasks/{id}/status", srv.UpdateTaskStatus)
	r.Delete("/tasks/{id}", srv.DeleteTask)
	r.Post("/tasks/{id}/retry", srv.RetryTask)
	r.Get("/tasks/{id}/lineage", srv.GetTaskLineage)
	r.Get("/handlers", srv.ListHandlers)

	httpSrv := &http.Server{Handler: r}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go httpSrv.Serve(ln)
	t.Cleanup(func() { httpSrv.Close() })

	baseURL := "http://" + ln.Addr().String()
	return &testEnv{baseURL: baseURL, client: &http.Client{Timeout: 10 * time.Second}}
}

func (e *testEnv) post(t *testing.T, path string, body interface{}) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := e.client.Post(e.baseURL+path, "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	return resp
}

func (e *testEnv) get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := e.client.Get(e.baseURL + path)
	require.NoError(t, err)
	return resp
}

func (e *testEnv) patch(t *testing.T, path string, body interface{}) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPatch, e.baseURL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	require.NoError(t, err)
	return resp
}

func (e *testEnv) delete(t *testing.T, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, e.baseURL+path, nil)
	resp, err := e.client.Do(req)
	require.NoError(t, err)
	return resp
}

func createTaskBody(id string) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "cluster": "e2e-cluster", "handler": "e2e-handler",
		"namespace": "kape-system", "event_id": "evt-" + id,
		"event_source": "alertmanager", "event_type": "kape.events.alertmanager",
		"event_raw":   map[string]interface{}{"specversion": "1.0"},
		"status":      "Processing", "dry_run": false,
		"received_at": time.Now().UTC().Format(time.RFC3339),
	}
}

func TestE2E_FullTaskLifecycle(t *testing.T) {
	env := startServer(t)

	// Create
	resp := env.post(t, "/tasks", createTaskBody("01E2E"))
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Get
	resp = env.get(t, "/tasks/01E2E")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&got)
	assert.Equal(t, "01E2E", got["id"])
	assert.Equal(t, "Processing", got["status"])

	// Update to Completed
	resp = env.patch(t, "/tasks/01E2E/status", map[string]interface{}{
		"status":       "Completed",
		"completed_at": time.Now().UTC().Format(time.RFC3339),
		"duration_ms":  1234,
	})
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify terminal state
	resp = env.get(t, "/tasks/01E2E")
	json.NewDecoder(resp.Body).Decode(&got)
	assert.Equal(t, "Completed", got["status"])
}

func TestE2E_InvalidTransitionRejected(t *testing.T) {
	env := startServer(t)

	env.post(t, "/tasks", createTaskBody("01INV"))

	// Complete it
	env.patch(t, "/tasks/01INV/status", map[string]interface{}{
		"status": "Completed",
	})

	// Attempt invalid transition back to Processing
	resp := env.patch(t, "/tasks/01INV/status", map[string]interface{}{"status": "Processing"})
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestE2E_ListFilterByHandler(t *testing.T) {
	env := startServer(t)

	b := createTaskBody("01LA")
	b["handler"] = "handler-a"
	env.post(t, "/tasks", b)

	b = createTaskBody("01LB")
	b["handler"] = "handler-a"
	env.post(t, "/tasks", b)

	b = createTaskBody("01LC")
	b["handler"] = "handler-b"
	env.post(t, "/tasks", b)

	resp := env.get(t, "/tasks?handler=handler-a")
	var list map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&list)
	tasks := list["tasks"].([]interface{})
	assert.Len(t, tasks, 2)
}

func TestE2E_StaleEventDiscard(t *testing.T) {
	env := startServer(t)

	env.post(t, "/tasks", createTaskBody("01STALE"))
	resp := env.delete(t, "/tasks/01STALE")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	resp = env.get(t, "/tasks/01STALE")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestE2E_SSEStreamDeliversTask(t *testing.T) {
	env := startServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, env.baseURL+"/tasks/stream", nil)
	sseResp, err := env.client.Do(req)
	require.NoError(t, err)
	defer sseResp.Body.Close()

	// Give subscriber time to register
	time.Sleep(20 * time.Millisecond)

	// Publish a task in parallel
	go func() {
		time.Sleep(50 * time.Millisecond)
		env.post(t, "/tasks", createTaskBody("01SSE"))
	}()

	scanner := bufio.NewScanner(sseResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			assert.Contains(t, line, "01SSE")
			return
		}
	}
	t.Fatal("did not receive SSE event within timeout")
}

func TestE2E_LineageChain(t *testing.T) {
	env := startServer(t)

	// Create root task
	env.post(t, "/tasks", createTaskBody("01ROOT"))

	// Fail the root
	env.patch(t, "/tasks/01ROOT/status", map[string]interface{}{"status": "Failed"})

	// Create retry with retry_of
	b := createTaskBody("01RETRY")
	b["retry_of"] = "01ROOT"
	env.post(t, "/tasks", b)

	resp := env.get(t, "/tasks/01RETRY/lineage")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var chain []map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&chain)
	require.Len(t, chain, 2)
	assert.Equal(t, "01ROOT", chain[0]["id"])
	assert.Equal(t, "01RETRY", chain[1]["id"])
}

func TestE2E_BulkTimeout(t *testing.T) {
	env := startServer(t)

	for _, id := range []string{"01BT1", "01BT2", "01BT3"} {
		env.post(t, "/tasks", createTaskBody(id))
	}

	resp := env.patch(t, "/tasks/bulk/status", map[string]interface{}{
		"status":             "Timeout",
		"older_than_seconds": 0, // all Processing tasks
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var bulkResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&bulkResp)
	affected := bulkResp["affected_ids"].([]interface{})
	assert.GreaterOrEqual(t, len(affected), 3)

	// Confirm statuses
	for _, id := range []string{"01BT1", "01BT2", "01BT3"} {
		r := env.get(t, "/tasks/"+id)
		var got map[string]interface{}
		json.NewDecoder(r.Body).Decode(&got)
		assert.Equal(t, "Timeout", got["status"], "task %s should be Timeout", id)
	}
}
