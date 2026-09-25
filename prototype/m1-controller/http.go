package main

import (
	"crypto/rand"
	"log"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
)

// NewRouter wires the localhost:8081 API:
//
//	POST   /sessions            -> create sandbox + pi session, return {id}
//	POST   /sessions/{id}/prompt {"message": ...} -> 202 (queued to pi stdin)
//	GET    /sessions/{id}/events -> SSE: buffered replay then live
//	DELETE /sessions/{id}       -> abort exec + delete sandbox
func NewRouter(mgr *SessionManager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", func(w http.ResponseWriter, r *http.Request) {
		sess, err := mgr.Create(r.Context())
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, sess)
	})
	mux.HandleFunc("POST /sessions/{id}/prompt", func(w http.ResponseWriter, r *http.Request) {
		sess := mgr.Get(r.PathValue("id"))
		if sess == nil {
			httpError(w, http.StatusNotFound, fmt.Errorf("unknown session %q", r.PathValue("id")))
			return
		}
		var body struct {
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Message == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("body must be {\"message\": string}"))
			return
		}
		if err := sess.Prompt(body.Message); err != nil {
			httpError(w, http.StatusConflict, err)
			return
		}
		log.Printf("[session %s] prompt accepted: %q", sess.ID, body.Message)
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /sessions/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		sess := mgr.Get(r.PathValue("id"))
		if sess == nil {
			httpError(w, http.StatusNotFound, fmt.Errorf("unknown session %q", r.PathValue("id")))
			return
		}
		serveSSE(w, r, sess)
	})
	mux.HandleFunc("DELETE /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := mgr.Delete(r.Context(), r.PathValue("id")); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// serveSSE streams events as text/event-stream: buffered replay, then live.
func serveSSE(w http.ResponseWriter, r *http.Request, sess *Session) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	replay, live, unsub := sess.hub.Subscribe()
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
