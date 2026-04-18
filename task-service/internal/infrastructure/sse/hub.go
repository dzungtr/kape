package sse

import (
	"sync"

	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/oklog/ulid/v2"
)

// Hub implements domain.Stream. It fan-outs published Tasks to all subscribers.
// Slow subscribers receive dropped messages — their channel is buffered and
// a non-blocking send is used.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[string]chan *task.Task
}

func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[string]chan *task.Task),
	}
}

// Publish sends t to every current subscriber. Drops silently on full buffers.
func (h *Hub) Publish(t *task.Task) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, ch := range h.subscribers {
		select {
		case ch <- t:
		default: // slow client — drop
		}
	}
}

// Subscribe registers a new subscriber and returns its channel plus an
// unsubscribe function. The caller must call the unsubscribe function when done.
func (h *Hub) Subscribe() (<-chan *task.Task, func()) {
	id := ulid.Make().String()
	ch := make(chan *task.Task, 16)

	h.mu.Lock()
	h.subscribers[id] = ch
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		delete(h.subscribers, id)
		h.mu.Unlock()
		close(ch)
	}
}
