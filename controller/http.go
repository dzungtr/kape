package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// NewRouter wires the agent API (domain language: sandbox agent, never session):
//
//	POST   /agents                 -> create sandbox + pi agent, return {id,...,status}
//	GET    /agents/{id}            -> agent detail incl. status
//	POST   /agents/{id}/prompt     {"message": ...} -> 202 (409 if turn in flight)
//	POST   /agents/{id}/abort      -> stop the in-flight turn
//	GET    /agents/{id}/events     -> SSE: buffered replay then live
//	DELETE /agents/{id}            -> abort exec + delete sandbox
func NewRouter(mgr *AgentManager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /agents", func(w http.ResponseWriter, r *http.Request) {
		agent, err := mgr.Create(r.Context())
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, agent)
	})
	mux.HandleFunc("GET /agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		agent := mgr.Get(r.PathValue("id"))
		if agent == nil {
			httpError(w, http.StatusNotFound, errUnknownAgent(r.PathValue("id")))
			return
		}
		writeJSON(w, http.StatusOK, struct {
			ID      string      `json:"id"`
			Sandbox string      `json:"sandbox"`
			Status  AgentStatus `json:"status"`
			Created interface{} `json:"created"`
		}{agent.ID, agent.Sandbox, agent.Status(), agent.Created})
	})
	mux.HandleFunc("POST /agents/{id}/prompt", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if mgr.Get(id) == nil {
			httpError(w, http.StatusNotFound, errUnknownAgent(id))
			return
		}
		var body struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Message == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("body must be {\"message\": string}"))
			return
		}
		if err := mgr.Prompt(id, body.Message); err != nil {
			httpError(w, statusForErr(err), err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /agents/{id}/abort", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if mgr.Get(id) == nil {
			httpError(w, http.StatusNotFound, errUnknownAgent(id))
			return
		}
		if err := mgr.Abort(id); err != nil {
			httpError(w, statusForErr(err), err)
			return
		}
		log.Printf("[http] abort accepted for agent %s", id)
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /agents/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		agent := mgr.Get(r.PathValue("id"))
		if agent == nil {
			httpError(w, http.StatusNotFound, errUnknownAgent(r.PathValue("id")))
			return
		}
		serveSSE(w, r, agent)
	})
	mux.HandleFunc("DELETE /agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := mgr.Delete(r.Context(), r.PathValue("id")); err != nil {
			httpError(w, statusForErr(err), err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// statusForErr maps manager errors to HTTP status: unknown agent → 404,
// in-flight prompt policy → 409, everything else → 500.
func statusForErr(err error) int {
	if err == nil {
		return http.StatusInternalServerError
	}
	// Sentinel-wrapped errors carry a distinguishable prefix.
	msg := err.Error()
	if strings.HasPrefix(msg, "unknown agent") {
		return http.StatusNotFound
	}
	if strings.HasPrefix(msg, "agent ") && strings.Contains(msg, "turn in flight") {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// serveSSE streams events as text/event-stream: buffered replay, then live.
func serveSSE(w http.ResponseWriter, r *http.Request, agent *Agent) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	replay, live, unsub := agent.hub.Subscribe()
	defer unsub()
	for _, raw := range replay {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return
		}
	}
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case raw, ok := <-live:
			if !ok {
				fmt.Fprint(w, "event: closed\n\n")
				flusher.Flush()
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, err error) {
	log.Printf("[http] %d: %v", status, err)
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// randID returns a short random hex id with the given prefix.
func randID(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(b)
}
