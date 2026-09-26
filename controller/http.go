package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	// MCP transport: Streamable HTTP on the same listener (issue #178).
	// One AgentManager instance serves both transports — the handler core is
	// shared, the MCP layer in mcpapi.go is a thin transport over it.
	mcpSrv := NewMCPServer(mgr)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil))
	mux.HandleFunc("POST /agents", func(w http.ResponseWriter, r *http.Request) {
		var profile CreateProfile
		if r.ContentLength != 0 {
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if err := dec.Decode(&profile); err != nil {
				httpFieldError(w, fieldErr("body", "must be an object with optional name, image, resources, provider, model"))
				return
			}
		}
		agent, err := mgr.Create(r.Context(), &profile)
		if err != nil {
			if errors.Is(err, ErrValidation) {
				httpFieldError(w, err)
			} else {
				httpError(w, statusForErr(err), err)
			}
			return
		}
		writeJSON(w, http.StatusCreated, agentViewOf(agent))
	})
	mux.HandleFunc("GET /agents/{id}", func(w http.ResponseWriter, r *http.Request) {
		agent := mgr.Get(r.PathValue("id"))
		if agent == nil {
			httpError(w, http.StatusNotFound, errUnknownAgent(r.PathValue("id")))
			return
		}
		writeJSON(w, http.StatusOK, agentViewOf(agent))
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
		// Pull mode is the default for query-cursor requests, but a client
		// asking for text/event-stream wants SSE re-attach (Last-Event-ID or
		// after) — route it to the stream before the pull check, per the #172
		// handoff ("SSE re-attach via Last-Event-ID or after").
		if isPullRequest(r.URL.Query()) && !isStreamRequest(r) {
			readEvents(w, r, agent)
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

// agentView is the JSON shape for create and get responses.
type agentView struct {
	ID      string      `json:"id"`
	Sandbox string      `json:"sandbox"`
	Status  AgentStatus `json:"status"`
	Created interface{} `json:"created"`
}

func agentViewOf(a *Agent) agentView {
	return agentView{a.ID, a.Sandbox, a.Status(), a.Created}
}

// statusForErr maps manager errors to HTTP status: validation (field-named)
// → 400, unknown agent → 404, prompt policy (turn in flight / not in an
// accepting state) → 409, everything else → 500. Mapping is typed via
// sentinel errors, never message text.
func statusForErr(err error) int {
	switch {
	case errors.Is(err, ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, ErrUnknownAgent):
		return http.StatusNotFound
	case errors.Is(err, ErrTurnInFlight), errors.Is(err, ErrNotReady):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// httpFieldError writes a 400 naming the offending field so callers (REST
// and the MCP create tool) can surface precise validation errors.
func httpFieldError(w http.ResponseWriter, err error) {
	var fe *FieldError
	field := ""
	if errors.As(err, &fe) {
		field = fe.Field
	}
	log.Printf("[http] 400: %v", err)
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "field": field})
}

// defaultReadLimit bounds a read_events batch when no limit is given.
const defaultReadLimit = 1000

// parseReadParams validates the read_events query. Returns (nil, "") when no
// pull-mode params are present (SSE mode), (nil, msg) on a 400-worthy problem.
func parseReadParams(q url.Values) (*readParams, string) {
	_, hasAfter := q["after"]
	_, hasLimit := q["limit"]
	_, hasLast := q["last"]
	_, hasTypes := q["types"]
	if !hasAfter && !hasLimit && !hasLast && !hasTypes {
		return nil, ""
	}
	p := &readParams{limit: defaultReadLimit}
	if hasAfter && hasLast {
		return nil, "after and last are mutually exclusive"
	}
	if hasLast && hasLimit {
		return nil, "last and limit are mutually exclusive"
	}
	if hasAfter {
		after, err := strconv.ParseUint(q.Get("after"), 10, 64)
		if err != nil {
			return nil, "after must be a non-negative integer"
		}
		p.after = after
	}
	if hasLimit {
		limit, err := strconv.Atoi(q.Get("limit"))
		if err != nil || limit <= 0 {
			return nil, "limit must be a positive integer"
		}
		p.limit = limit
	}
	if hasLast {
		last, err := strconv.Atoi(q.Get("last"))
		if err != nil || last < 0 {
			return nil, "last must be a non-negative integer"
		}
		p.last = last
	}
	if hasTypes {
		for _, t := range strings.Split(q.Get("types"), ",") {
			if t = strings.TrimSpace(t); t != "" {
				p.types = append(p.types, t)
			}
		}
	}
	return p, ""
}

// isPullRequest reports whether the query selects read_events pull mode:
// presence of any pull param — even an invalid one, which readEvents must
// 400 rather than silently stream.
func isPullRequest(q url.Values) bool {
	for _, k := range []string{"after", "limit", "last", "types"} {
		if _, ok := q[k]; ok {
			return true
		}
	}
	return false
}

// isStreamRequest reports whether the client asked for the SSE stream via
// the Accept header — the signal that an after-cursor request is an SSE
// re-attach rather than a pull.
func isStreamRequest(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

// readParams is the parsed read_events query: after/limit/last/types.
type readParams struct {
	after uint64
	limit int
	last  int
	types []string
}

// readEvents handles read_events pull mode. The response carries the events
// (each {seq, type, data}; data is the verbatim record) and truncated: true
// once any events have been evicted from the buffer ring.
func readEvents(w http.ResponseWriter, r *http.Request, agent *Agent) {
	p, msg := parseReadParams(r.URL.Query())
	if p == nil {
		httpError(w, http.StatusBadRequest, fmt.Errorf("%s", msg))
		return
	}
	var res ReadResult
	if _, hasLast := r.URL.Query()["last"]; hasLast {
		res = agent.hub.ReadLast(p.last, p.types)
	} else {
		res = agent.hub.Read(p.after, p.limit, p.types)
	}
	if res.Events == nil {
		res.Events = []Event{} // empty poll serializes as [], never null
	}
	writeJSON(w, http.StatusOK, readEventsResponse{
		AgentID:   agent.ID,
		Events:    res.Events,
		Truncated: res.Truncated,
	})
}

// readEventsResponse is the JSON shape of read_events pull mode.
type readEventsResponse struct {
	AgentID   string  `json:"agent_id"`
	Events    []Event `json:"events"`
	Truncated bool    `json:"truncated"`
}

// serveSSE streams events as text/event-stream: buffered replay from the
// cursor (Last-Event-ID header or after query — re-attach mid-turn), then
// live. Each event carries id: <seq> so clients can resume from it.
func serveSSE(w http.ResponseWriter, r *http.Request, agent *Agent) {
	cursor, errMsg := sseCursor(r)
	if errMsg != "" {
		httpError(w, http.StatusBadRequest, fmt.Errorf("%s", errMsg))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	replay, live, unsub := agent.hub.Subscribe(cursor)
	defer unsub()
	for i := range replay {
		if !writeSSEEvent(w, &replay[i]) {
			return
		}
	}
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-live:
			if !ok {
				fmt.Fprint(w, "event: closed\n\n")
				flusher.Flush()
				return
			}
			if !writeSSEEvent(w, ev) {
				return
			}
			flusher.Flush()
		}
	}
}

// sseCursor resolves the SSE re-attach cursor: Last-Event-ID header or the
// after query param (both together -> 400). Zero cursor = replay from start.
func sseCursor(r *http.Request) (uint64, string) {
	lid := r.Header.Get("Last-Event-ID")
	after := r.URL.Query().Get("after")
	if lid != "" && after != "" {
		return 0, "Last-Event-ID and after are mutually exclusive"
	}
	v := lid
	if v == "" {
		v = after
	}
	if v == "" {
		return 0, ""
	}
	cursor, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, "cursor must be a non-negative integer"
	}
	return cursor, ""
}

// writeSSEEvent frames one event: id: <seq>, data: <verbatim record>.
func writeSSEEvent(w io.Writer, ev *Event) bool {
	_, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.Seq, ev.Data)
	return err == nil
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
