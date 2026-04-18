package http

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/kape-io/kape/task-service/internal/domain/task"
)

// SSEHandler serves GET /tasks/stream as a Server-Sent Events stream.
type SSEHandler struct {
	stream task.Stream
}

func NewSSEHandler(stream task.Stream) *SSEHandler {
	return &SSEHandler{stream: stream}
}

func (h *SSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsub := h.stream.Subscribe()
	defer unsub()

	for {
		select {
		case <-r.Context().Done():
			return
		case t, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(domainToGenTask(t))
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
