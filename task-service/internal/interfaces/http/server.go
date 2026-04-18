package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kape-io/kape/task-service/internal/application/command"
	"github.com/kape-io/kape/task-service/internal/application/query"
	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/kape-io/kape/task-service/internal/interfaces/http/gen"
)

// Server implements the HTTP handlers for the task service API.
type Server struct {
	createTask   *command.CreateTaskCommand
	updateStatus *command.UpdateStatusCommand
	deleteTask   *command.DeleteTaskCommand
	bulkTimeout  *command.BulkUpdateStatusCommand
	getTask      *query.GetTaskQuery
	listTasks    *query.ListTasksQuery
	taskLineage  *query.TaskLineageQuery
	handlerStats *query.HandlerStatsQuery
}

func NewServer(
	createTask *command.CreateTaskCommand,
	updateStatus *command.UpdateStatusCommand,
	deleteTask *command.DeleteTaskCommand,
	bulkTimeout *command.BulkUpdateStatusCommand,
	getTask *query.GetTaskQuery,
	listTasks *query.ListTasksQuery,
	taskLineage *query.TaskLineageQuery,
	handlerStats *query.HandlerStatsQuery,
) *Server {
	return &Server{
		createTask: createTask, updateStatus: updateStatus,
		deleteTask: deleteTask, bulkTimeout: bulkTimeout,
		getTask: getTask, listTasks: listTasks,
		taskLineage: taskLineage, handlerStats: handlerStats,
	}
}

// Routes registers all task-service HTTP routes onto r.
func (s *Server) Routes(r chi.Router, sseHandler *SSEHandler) {
	r.Post("/tasks", s.CreateTask)
	r.Get("/tasks", s.ListTasks)
	r.Get("/tasks/stream", sseHandler.ServeHTTP)
	r.Get("/tasks/decisions", s.GetDecisions)
	r.Patch("/tasks/bulk/status", s.BulkUpdateStatus)
	r.Get("/tasks/{id}", s.GetTask)
	r.Patch("/tasks/{id}/status", s.UpdateTaskStatus)
	r.Delete("/tasks/{id}", s.DeleteTask)
	r.Post("/tasks/{id}/retry", s.RetryTask)
	r.Get("/tasks/{id}/lineage", s.GetTaskLineage)
	r.Get("/handlers", s.ListHandlers)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// CreateTask handles POST /tasks
func (s *Server) CreateTask(w http.ResponseWriter, r *http.Request) {
	var req gen.CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	t, err := s.createTask.Execute(r.Context(), command.CreateTaskInput{
		Params: task.CreateParams{
			ID: req.Id, Cluster: req.Cluster, Handler: req.Handler,
			Namespace: req.Namespace, EventID: req.EventId,
			EventSource: req.EventSource, EventType: req.EventType,
			EventRaw: task.EventRaw(req.EventRaw), DryRun: req.DryRun,
			RetryOf: req.RetryOf, OtelTraceID: req.OtelTraceId,
			ReceivedAt: req.ReceivedAt,
		},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, domainToGenTask(t))
}

// GetTask handles GET /tasks/{id}
func (s *Server) GetTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.getTask.Execute(r.Context(), id)
	if errors.Is(err, task.ErrNotFound) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domainToGenTask(t))
}

// UpdateTaskStatus handles PATCH /tasks/{id}/status
func (s *Server) UpdateTaskStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req gen.UpdateStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	fields := task.UpdateFields{
		CompletedAt: req.CompletedAt,
	}
	if req.DurationMs != nil {
		ms := int(*req.DurationMs)
		fields.DurationMs = &ms
	}
	if req.SchemaOutput != nil {
		so := task.SchemaOutput(*req.SchemaOutput)
		fields.SchemaOutput = &so
	}
	if req.Actions != nil {
		actions := genActionsToDomain(req.Actions)
		fields.Actions = &actions
	}
	// gen.UpdateStatusRequest.Error is a value type — check if it's non-empty
	if req.Error.Type != "" {
		fields.Error = &task.TaskError{
			Type: req.Error.Type, Detail: req.Error.Detail,
			Schema: req.Error.Schema, Raw: req.Error.Raw, Traceback: req.Error.Traceback,
		}
	}

	t, err := s.updateStatus.Execute(r.Context(), command.UpdateStatusInput{
		ID:     id,
		Status: task.TaskStatus(req.Status),
		Fields: fields,
	})
	if errors.Is(err, task.ErrNotFound) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if errors.Is(err, task.ErrInvalidTransition) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domainToGenTask(t))
}

// DeleteTask handles DELETE /tasks/{id}
func (s *Server) DeleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := s.deleteTask.Execute(r.Context(), id)
	if errors.Is(err, task.ErrNotFound) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListTasks handles GET /tasks
func (s *Server) ListTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 50
	if v := q.Get("limit"); v != "" {
		fmt.Sscanf(v, "%d", &limit)
	}
	var since time.Time
	if v := q.Get("since"); v != "" {
		since, _ = time.Parse(time.RFC3339, v)
	}

	result, err := s.listTasks.Execute(r.Context(), task.ListFilter{
		Handler: q.Get("handler"),
		Status:  task.TaskStatus(q.Get("status")),
		Since:   since,
		Sort:    q.Get("sort"),
		Limit:   limit,
		Cursor:  q.Get("cursor"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	genTasks := make([]gen.Task, len(result.Tasks))
	for i, t := range result.Tasks {
		genTasks[i] = domainToGenTask(t)
	}
	writeJSON(w, http.StatusOK, gen.TaskList{
		Tasks:      genTasks,
		Total:      int32(result.Total),
		NextCursor: result.NextCursor,
	})
}

// GetTaskLineage handles GET /tasks/{id}/lineage
func (s *Server) GetTaskLineage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	chain, err := s.taskLineage.Execute(r.Context(), id)
	if errors.Is(err, task.ErrNotFound) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	genChain := make([]gen.Task, len(chain))
	for i, t := range chain {
		genChain[i] = domainToGenTask(t)
	}
	writeJSON(w, http.StatusOK, genChain)
}

// BulkUpdateStatus handles PATCH /tasks/bulk/status
func (s *Server) BulkUpdateStatus(w http.ResponseWriter, r *http.Request) {
	var req gen.BulkTimeoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ids, err := s.bulkTimeout.Execute(r.Context(), command.BulkUpdateStatusInput{
		IDs:    req.Ids,
		Status: task.TaskStatus(req.Status),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, gen.BulkTimeoutResponse{AffectedIds: ids})
}

// ListHandlers handles GET /handlers
func (s *Server) ListHandlers(w http.ResponseWriter, r *http.Request) {
	sinceStr := r.URL.Query().Get("since")
	since, err := time.Parse(time.RFC3339, sinceStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}
	stats, err := s.handlerStats.Execute(r.Context(), since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result := make([]gen.HandlerStat, len(stats))
	for i, s := range stats {
		breakdown := make(map[string]int32, len(s.StatusBreakdown))
		for k, v := range s.StatusBreakdown {
			breakdown[string(k)] = int32(v)
		}
		result[i] = gen.HandlerStat{
			Handler: s.Handler, EventCount: int32(s.EventCount),
			StatusBreakdown: breakdown,
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// GetDecisions handles GET /tasks/decisions
func (s *Server) GetDecisions(w http.ResponseWriter, r *http.Request) {
	sinceStr := r.URL.Query().Get("since")
	since, err := time.Parse(time.RFC3339, sinceStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since parameter")
		return
	}
	handler := r.URL.Query().Get("handler")

	result, err := s.listTasks.Execute(r.Context(), task.ListFilter{
		Handler: handler,
		Since:   since,
		Limit:   1000,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	distribution := map[string]int32{}
	for _, t := range result.Tasks {
		if t.SchemaOutput != nil {
			if decision, ok := (*t.SchemaOutput)["decision"].(string); ok {
				distribution[decision]++
			}
		}
	}
	writeJSON(w, http.StatusOK, gen.DecisionDistribution{
		Handler:      handler,
		Since:        since,
		Distribution: distribution,
	})
}

// RetryTask handles POST /tasks/{id}/retry — stubbed until Phase 7
func (s *Server) RetryTask(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

// domainToGenTask maps a domain Task to the generated API Task type.
func domainToGenTask(t *task.Task) gen.Task {
	var durationMs *int32
	if t.DurationMs != nil {
		v := int32(*t.DurationMs)
		durationMs = &v
	}

	g := gen.Task{
		Id:          t.ID,
		Cluster:     t.Cluster,
		Handler:     t.Handler,
		Namespace:   t.Namespace,
		EventId:     t.EventID,
		EventSource: t.EventSource,
		EventType:   t.EventType,
		EventRaw:    map[string]interface{}(t.EventRaw),
		Status:      gen.TaskStatus(t.Status),
		DryRun:      t.DryRun,
		RetryOf:     t.RetryOf,
		OtelTraceId: t.OtelTraceID,
		ReceivedAt:  t.ReceivedAt,
		CompletedAt: t.CompletedAt,
		DurationMs:  durationMs,
	}
	if t.SchemaOutput != nil {
		so := map[string]interface{}(*t.SchemaOutput)
		g.SchemaOutput = &so
	}
	if t.Actions != nil {
		actions := make([]gen.ActionResult, len(*t.Actions))
		for i, a := range *t.Actions {
			actions[i] = gen.ActionResult{Name: a.Name, Type: a.Type, Status: a.Status, DryRun: a.DryRun, Error: a.Error}
		}
		g.Actions = &actions
	}
	if t.Error != nil {
		g.Error = gen.TaskError{
			Type: t.Error.Type, Detail: t.Error.Detail,
			Schema: t.Error.Schema, Raw: t.Error.Raw, Traceback: t.Error.Traceback,
		}
	}
	return g
}

func genActionsToDomain(genActions *[]gen.ActionResult) task.Actions {
	if genActions == nil {
		return nil
	}
	result := make(task.Actions, len(*genActions))
	for i, a := range *genActions {
		result[i] = task.ActionResult{Name: a.Name, Type: a.Type, Status: a.Status, DryRun: a.DryRun, Error: a.Error}
	}
	return result
}
