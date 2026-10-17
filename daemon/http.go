package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Command types (ADR-0083 D3/D4). The daemon validates and enqueues; it never
// tracks settlement — settlement flows via the events stream.
const (
	CommandPrompt     = "prompt"
	CommandSteer      = "steer"
	CommandAbort      = "abort"
	CommandClearQueue = "clear_queue"
)

var commandTypes = map[string]bool{CommandPrompt: true, CommandSteer: true, CommandAbort: true, CommandClearQueue: true}

// Command is the POST /commands body (and GET /commands/{id} response).
type Command struct {
	ID      string          `json:"command_id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	Status  string          `json:"status"`
}

// CommandQueue is the in-memory command store: 202 on acceptance, settlement
// is not the daemon's business.
type CommandQueue struct {
	mu   sync.Mutex
	cmds map[string]Command
}

func NewCommandQueue() *CommandQueue { return &CommandQueue{cmds: map[string]Command{}} }

// Accept validates and stores the command.
func (q *CommandQueue) Accept(c Command) error {
	if c.ID == "" {
		return fieldErr("command_id", "must be non-empty")
	}
	if !commandTypes[c.Type] {
		return fieldErr("type", fmt.Sprintf("must be one of prompt, steer, abort, clear_queue (got %q)", c.Type))
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if prev, ok := q.cmds[c.ID]; ok {
		if prev.Type != c.Type {
			return fieldErr("command_id", "already accepted with a different type")
		}
		return nil // idempotent re-post
	}
	c.Status = "queued"
	q.cmds[c.ID] = c
	return nil
}

func (q *CommandQueue) Get(id string) (Command, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	c, ok := q.cmds[id]
	return c, ok
}

// Status is the GET /status body: reconnect preflight (ADR-0083 D6) —
// boot_id distinguishes partition from daemon death — extended by the E4/E8
// crash-ownership surface: spool_depth (records held in the spool window),
// pi_restarts (respawns since daemon boot), and the current respawn backoff
// ("0s" when none).
type Status struct {
	AgentID    string `json:"agent_id"`
	BootID     string `json:"boot_id"`
	PiRunning  bool   `json:"pi_running"`
	SpoolDepth int    `json:"spool_depth"`
	PiRestarts int    `json:"pi_restarts"`
	Backoff    string `json:"backoff"`
}

// NewRouter wires the D3 endpoint surface:
//
//	GET  /status           {agent_id, boot_id, pi_running}
//	GET  /records?after=&wait=   long-poll spool read
//	POST /commands         202 + Location: /commands/{id}
//	GET  /commands/{id}
//	GET  /healthz          (unauthenticated)
//
// Auth (ADR-0083 D5): placeholder static bearer token from env; requests
// without a credential → 401. Scheme itself is deferred.
func NewRouter(agentID, bootID string, spool *Spool, pi *Pi, queue *CommandQueue, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, Status{
			AgentID: agentID, BootID: bootID, PiRunning: pi.Running(),
			SpoolDepth: spool.Depth(), PiRestarts: pi.Restarts(),
			Backoff: pi.Backoff().String(),
		})
	})
	mux.HandleFunc("GET /records", func(w http.ResponseWriter, r *http.Request) {
		after, err := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
		if err != nil {
			httpFieldError(w, fieldErr("after", "must be an unsigned integer"))
			return
		}
		wait := 0 * time.Second
		if ws := r.URL.Query().Get("wait"); ws != "" {
			wait, err = time.ParseDuration(ws)
			if err != nil || wait < 0 {
				httpFieldError(w, fieldErr("wait", "must be a duration (e.g. 25s)"))
				return
			}
		}
		recs := spool.Read(after, wait)
		if recs == nil {
			recs = []Record{}
		}
		writeJSON(w, http.StatusOK, map[string][]Record{"records": recs})
	})
	mux.HandleFunc("POST /commands", func(w http.ResponseWriter, r *http.Request) {
		var c Command
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			httpFieldError(w, fieldErr("body", "must be an object {command_id, type, payload}"))
			return
		}
		if err := queue.Accept(c); err != nil {
			httpFieldError(w, err)
			return
		}
		// Delivery: acceptance alone does not reach pi. Forward the accepted
		// command to pi's stdin here (single call site). Idempotent re-posts
		// re-deliver, which lets a client retry a failed send without a new
		// command id (Accept is a no-op for a same-type re-post).
		if err := pi.SendCommand(context.Background(), c); err != nil {
			httpError(w, http.StatusInternalServerError, fmt.Errorf("deliver command to pi: %w", err))
			return
		}
		stored, _ := queue.Get(c.ID)
		c = stored
		w.Header().Set("Location", "/commands/"+c.ID)
		writeJSON(w, http.StatusAccepted, c)
	})
	mux.HandleFunc("GET /commands/{id}", func(w http.ResponseWriter, r *http.Request) {
		c, ok := queue.Get(r.PathValue("id"))
		if !ok {
			httpError(w, http.StatusNotFound, errors.New("unknown command"))
			return
		}
		writeJSON(w, http.StatusOK, c)
	})
	return auth(mux, token)
}

// auth is the D5 placeholder: static bearer token; missing/wrong credential → 401.
func auth(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" { // liveness stays unauthenticated
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || got != token {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kape-daemon"`)
			httpError(w, http.StatusUnauthorized, errors.New("missing or invalid Authorization credential"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func httpFieldError(w http.ResponseWriter, err error) {
	field := ""
	var fe *FieldError
	if errors.As(err, &fe) {
		field = fe.Field
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "field": field})
}

// FieldError + fieldErr mirror the controller's validation contract
// (controller/config.go): validation errors name the offending field.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Reason) }

func fieldErr(field, reason string) error { return &FieldError{Field: field, Reason: reason} }
