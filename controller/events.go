package main

import (
	"encoding/json"
	"sync"
)

// Event is one entry in an agent's event buffer. Pi RPC records are carried
// verbatim in Data (never re-wrapped); seq is the per-agent cursor assigned
// by the hub and is the only controller-added field at the envelope layer.
// Lifecycle events additionally carry {agent_id, status, seq} inside Data.
type Event struct {
	Seq  uint64          `json:"seq"`
	Type string          `json:"type,omitempty"`
	Data json.RawMessage `json:"data"`
}

// eventBufferSize is the per-agent ring cap: a runaway agent cannot OOM the
// controller. Once full, the oldest events are evicted and readers see
// truncated: true (v1 semantics: history is in-memory only, restart drops it).
const eventBufferSize = 4096

// EventHub buffers an agent's events with per-agent monotonic seq and fans
// out to SSE subscribers. The slow-subscriber drop policy applies only to
// the live channel; pulls (Read/ReadLast) always read the buffer.
type EventHub struct {
	mu          sync.Mutex
	buffer      []Event
	cap         int
	nextSeq     uint64
	evicted     uint64
	subscribers map[int]chan *Event
	nextSub     int
	closed      bool
}

func NewEventHub() *EventHub { return newEventHub(eventBufferSize) }

func newEventHub(cap int) *EventHub {
	return &EventHub{cap: cap, subscribers: map[int]chan *Event{}}
}

// Publish appends a verbatim record (pi RPC record, stderr/exec_exit notice)
// to the buffer, assigns the next seq, and fans out. Returns the assigned seq.
func (h *EventHub) Publish(raw json.RawMessage) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0
	}
	h.nextSeq++
	ev := Event{Seq: h.nextSeq, Type: eventTypeOf(raw), Data: append(json.RawMessage(nil), raw...)}
	h.append(ev)
	h.fanout(&ev)
	return ev.Seq
}

// PublishLifecycle appends a controller lifecycle event whose Data carries
// {"type", "agent_id", "status", "seq", ...fields} — the seq both inside the
// payload (per the event contract) and on the envelope.
func (h *EventHub) PublishLifecycle(eventType string, fields map[string]interface{}) *Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.nextSeq++
	fields["type"] = eventType
	fields["seq"] = h.nextSeq
	ev := Event{Seq: h.nextSeq, Type: eventType, Data: mustJSON(fields)}
	h.append(ev)
	h.fanout(&ev)
	return &ev
}

// append stores ev and evicts the oldest when the ring cap is reached.
// Caller holds h.mu.
func (h *EventHub) append(ev Event) {
	if len(h.buffer) == h.cap {
		h.buffer = h.buffer[1:]
		h.evicted++
	}
	h.buffer = append(h.buffer, ev)
}

// fanout delivers ev to live subscribers; a subscriber whose channel is full
// is skipped (drop policy — live SSE only, never the buffer). Caller holds h.mu.
func (h *EventHub) fanout(ev *Event) {
	for _, ch := range h.subscribers {
		select {
		case ch <- ev:
		default: // slow subscriber: drop rather than stall the pi stream
		}
	}
}

// Subscribe returns the buffered replay after the given cursor (all events
// with seq > after) plus a live channel; the cursor guarantees no gaps and
// no duplicates across re-attach.
func (h *EventHub) Subscribe(after uint64) ([]Event, chan *Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var replay []Event
	for i := range h.buffer {
		if h.buffer[i].Seq > after {
			replay = append(replay, h.buffer[i])
		}
	}
	ch := make(chan *Event, 256)
	id := h.nextSub
	h.nextSub++
	h.subscribers[id] = ch
	return replay, ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subscribers, id)
	}
}

// Close marks the hub closed: no further publishes, buffered history stays
// readable (pulls still work after the agent is terminated).
func (h *EventHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
}

// ReadResult is a pull batch from the buffer.
type ReadResult struct {
	Events    []Event
	Truncated bool
}

// Read returns events with seq > after, filtered by types (empty = all), up
// to limit — the primary cursor mode: bounded batches with no gaps and no
// duplicates across polls.
func (h *EventHub) Read(after uint64, limit int, types []string) ReadResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	return ReadResult{Events: filterEvents(h.buffer, after, limit, types), Truncated: h.evicted > 0}
}

// ReadLast returns the final n events (after types filtering) — the REST-only
// tail glance, never an MCP tool.
func (h *EventHub) ReadLast(n int, types []string) ReadResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	evs := filterEvents(h.buffer, 0, 0, types)
	if len(evs) > n {
		evs = evs[len(evs)-n:]
	}
	return ReadResult{Events: evs, Truncated: h.evicted > 0}
}

// filterEvents selects seq > after matching types, up to limit (0 = no
// bound). Caller holds h.mu.
func filterEvents(buffer []Event, after uint64, limit int, types []string) []Event {
	wanted := map[string]bool{}
	for _, t := range types {
		wanted[t] = true
	}
	var out []Event
	for i := range buffer {
		ev := &buffer[i]
		if ev.Seq <= after {
			continue
		}
		if len(wanted) > 0 && !wanted[ev.Type] {
			continue
		}
		out = append(out, *ev)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

// Events returns a snapshot of the buffered events (test helper).
func (h *EventHub) Events() []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Event, len(h.buffer))
	copy(out, h.buffer)
	return out
}

// eventTypeOf extracts the "type" field of a JSON object record, if any.
func eventTypeOf(raw json.RawMessage) string {
	var rec struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return ""
	}
	return rec.Type
}
