package main

import (
	"encoding/json"
	"log"
	"sync"
)

// Event is one entry in an agent's durable event log. Pi RPC records are
// carried verbatim in Data (never re-wrapped); seq is the per-agent cursor
// assigned by the hub and is the only controller-added field at the envelope
// layer. Lifecycle events additionally carry {agent_id, status, seq} inside
// Data.
type Event struct {
	Seq  uint64          `json:"seq"`
	Type string          `json:"type,omitempty"`
	Data json.RawMessage `json:"data"`
}

// EventHub is the re-based event surface (issue #195): append-only durable
// history via an EventStore, per-agent monotonic seq that survives restart,
// replay-after-cursor from the store, and unchanged live fan-out to SSE
// subscribers (slow-subscriber drop applies to the live channel only; pulls
// always read the store). There is no ring cap and no eviction: the store
// keeps full history, so `truncated` is always false — the v1 in-memory-ring
// semantics are gone.
type EventHub struct {
	mu          sync.Mutex
	store       EventStore
	agentID     string
	nextSeq     uint64 // recovered from the store at construction; single writer per agent
	subscribers map[int]chan *Event
	nextSub     int
	closed      bool
}

// NewEventHub opens the hub for one agent against the durable store,
// recovering the seq space from the log. After a controller restart a new
// hub for the same agent id continues the seq with no gap and no reuse.
func NewEventHub(store EventStore, agentID string) (*EventHub, error) {
	next, err := store.NextSeq(agentID)
	if err != nil {
		return nil, err
	}
	return &EventHub{store: store, agentID: agentID, nextSeq: next, subscribers: map[int]chan *Event{}}, nil
}

// Publish appends a verbatim record (pi RPC record, stderr/exec_exit notice)
// to the durable log, assigns the next seq, and fans out. Returns the
// assigned seq. A failed store append is logged, fanned out to nobody, and
// consumes no seq — durability wins over availability.
func (h *EventHub) Publish(raw json.RawMessage) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0
	}
	ev := Event{Seq: h.nextSeq + 1, Type: eventTypeOf(raw), Data: append(json.RawMessage(nil), raw...)}
	if err := h.store.Append(h.agentID, ev); err != nil {
		log.Printf("[events] append agent %s seq %d failed: %v", h.agentID, ev.Seq, err)
		return 0
	}
	h.nextSeq++
	h.fanout(&ev)
	return ev.Seq
}

// PublishLifecycle appends a controller lifecycle event whose Data carries
// {"type", "agent_id", "status", "seq", ...fields} — the seq both inside the
// payload (per the event contract) and on the envelope. On a failed append
// it returns nil (no seq consumed, no fan-out).
func (h *EventHub) PublishLifecycle(eventType string, fields map[string]interface{}) *Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	fields["type"] = eventType
	fields["seq"] = h.nextSeq + 1
	ev := Event{Seq: h.nextSeq + 1, Type: eventType, Data: mustJSON(fields)}
	if err := h.store.Append(h.agentID, ev); err != nil {
		log.Printf("[events] append agent %s seq %d failed: %v", h.agentID, ev.Seq, err)
		return nil
	}
	h.nextSeq++
	h.fanout(&ev)
	return &ev
}

// fanout delivers ev to live subscribers; a subscriber whose channel is full
// is skipped (drop policy — live SSE only, never the stored history).
// Caller holds h.mu.
func (h *EventHub) fanout(ev *Event) {
	for _, ch := range h.subscribers {
		select {
		case ch <- ev:
		default: // slow subscriber: drop rather than stall the pi stream
		}
	}
}

// Subscribe returns the stored replay after the given cursor (all events
// with seq > after) plus a live channel; the cursor guarantees no gaps and
// no duplicates across re-attach and across controller restarts. Replay and
// registration happen under one lock, so no live event can fall between.
// A store read failure is returned as an error (with an empty replay) so the
// SSE/MCP handler can surface it instead of replying with a silent empty
// replay.
func (h *EventHub) Subscribe(after uint64) ([]Event, chan *Event, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	replay, err := h.store.Replay(h.agentID, after)
	if err != nil {
		log.Printf("[events] replay agent %s after %d failed: %v", h.agentID, after, err)
		return nil, nil, func() {}, err
	}
	ch := make(chan *Event, 256)
	id := h.nextSub
	h.nextSub++
	h.subscribers[id] = ch
	return replay, ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subscribers, id)
	}, nil
}

// Close marks the hub closed: no further publishes. Stored history stays
// readable — pulls still work after the agent is terminated.
func (h *EventHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
}

// ReadResult is a pull batch from the store.
type ReadResult struct {
	Events    []Event
	Truncated bool // always false: the durable log is never truncated (issue #195)
	// Err is the store read failure, if any. Handlers must surface it to the
	// client (HTTP 500 / MCP tool error) instead of returning a silent empty
	// or partial batch.
	Err error
}

// Read returns events with seq > after, filtered by types (empty = all), up
// to limit — the primary cursor mode: bounded batches with no gaps and no
// duplicates across polls.
func (h *EventHub) Read(after uint64, limit int, types []string) ReadResult {
	return h.ReadFiltered(after, limit, types, nil)
}

// ReadFiltered is Read with an exclusion set: events whose type is in
// exclude are dropped before the limit is applied, so the MCP read_events
// tool's non-delta default (exclude text_delta) still yields full batches of
// matching events — never short ones.
func (h *EventHub) ReadFiltered(after uint64, limit int, types, exclude []string) ReadResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	all, err := h.store.Replay(h.agentID, after)
	if err != nil {
		log.Printf("[events] read agent %s after %d failed: %v", h.agentID, after, err)
		return ReadResult{Err: err}
	}
	return ReadResult{Events: filterEventsExcluding(all, after, limit, types, exclude)}
}

// ReadLast returns the final n events (after types filtering) — the REST-only
// tail glance, never an MCP tool.
func (h *EventHub) ReadLast(n int, types []string) ReadResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	all, err := h.store.Replay(h.agentID, 0)
	if err != nil {
		log.Printf("[events] read agent %s failed: %v", h.agentID, err)
		return ReadResult{Err: err}
	}
	evs := filterEvents(all, 0, 0, types)
	if len(evs) > n {
		evs = evs[len(evs)-n:]
	}
	return ReadResult{Events: evs}
}

// filterEvents selects seq > after matching types, up to limit (0 = no
// bound). Caller holds h.mu.
func filterEvents(buffer []Event, after uint64, limit int, types []string) []Event {
	return filterEventsExcluding(buffer, after, limit, types, nil)
}

// filterEventsExcluding is filterEvents with an exclusion set (empty = no
// exclusions). Caller holds h.mu.
func filterEventsExcluding(buffer []Event, after uint64, limit int, types, exclude []string) []Event {
	wanted := map[string]bool{}
	for _, t := range types {
		wanted[t] = true
	}
	excluded := map[string]bool{}
	for _, t := range exclude {
		excluded[t] = true
	}
	var out []Event
	for i := range buffer {
		ev := &buffer[i]
		if ev.Seq <= after || excluded[ev.Type] {
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

// Events returns a snapshot of the stored events (test helper).
func (h *EventHub) Events() []Event {
	evs, err := h.store.Replay(h.agentID, 0)
	if err != nil {
		log.Printf("[events] read agent %s failed: %v", h.agentID, err)
	}
	return evs
}

// Truncated reports whether any events have been evicted. The durable log is
// never evicted, so this is always false — the field stays on the response
// only to keep the read_events contract unchanged (#195).
func (h *EventHub) Truncated() bool { return false }

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
