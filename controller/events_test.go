package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- seq ordering and cursor pulls ---

func TestSeqAssignedToEveryEventMonotonically(t *testing.T) {
	h := NewEventHub()
	h.PublishLifecycle("agent.provisioned", map[string]interface{}{"agent_id": "a1", "status": StatusCreating})
	h.Publish(json.RawMessage(`{"type":"message_update","content":"hi"}`))
	h.PublishLifecycle("agent.started", map[string]interface{}{"agent_id": "a1", "status": StatusReady})

	evs := h.Events()
	if len(evs) != 3 {
		t.Fatalf("got %d events, want 3", len(evs))
	}
	for i, want := range []uint64{1, 2, 3} {
		if evs[i].Seq != want {
			t.Fatalf("event %d seq = %d, want %d", i, evs[i].Seq, want)
		}
	}
	// Lifecycle events carry {agent_id, status, seq} inside the payload.
	var lc struct {
		Type    string      `json:"type"`
		AgentID string      `json:"agent_id"`
		Status  AgentStatus `json:"status"`
		Seq     uint64      `json:"seq"`
	}
	if err := json.Unmarshal(evs[0].Data, &lc); err != nil {
		t.Fatalf("lifecycle data: %v", err)
	}
	if lc.Type != "agent.provisioned" || lc.AgentID != "a1" || lc.Status != StatusCreating || lc.Seq != 1 {
		t.Fatalf("lifecycle payload = %+v, want agent.provisioned/a1/creating/seq 1", lc)
	}
	// Pi records stay verbatim — no seq injected into the payload.
	if string(evs[1].Data) != `{"type":"message_update","content":"hi"}` {
		t.Fatalf("pi record re-wrapped: %s", evs[1].Data)
	}
}

func TestCursorPollsAcrossBurstNoGapsNoDuplicates(t *testing.T) {
	h := NewEventHub()
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h.Publish(json.RawMessage(fmt.Sprintf(`{"type":"record","n":%d}`, i)))
		}(i)
	}
	wg.Wait()

	// Poll in bounded batches from cursor 0; batch tails must chain exactly.
	seen := map[uint64]bool{}
	var after uint64
	batches := 0
	for {
		res := h.Read(after, 37, nil)
		batches++
		for _, ev := range res.Events {
			if seen[ev.Seq] {
				t.Fatalf("duplicate seq %d across polls", ev.Seq)
			}
			if ev.Seq <= after {
				t.Fatalf("seq %d not greater than cursor %d", ev.Seq, after)
			}
			seen[ev.Seq] = true
			after = ev.Seq
		}
		if len(res.Events) < 37 {
			break
		}
	}
	if len(seen) != 200 {
		t.Fatalf("saw %d events across %d batches, want 200 with no gaps", len(seen), batches)
	}
}

func TestReadTypeFilter(t *testing.T) {
	h := NewEventHub()
	h.PublishLifecycle("agent.provisioned", map[string]interface{}{"agent_id": "a", "status": StatusCreating})
	h.Publish(json.RawMessage(`{"type":"text_delta","text":"x"}`))
	h.Publish(json.RawMessage(`{"type":"message_update","content":"y"}`))
	h.PublishLifecycle("agent.started", map[string]interface{}{"agent_id": "a", "status": StatusReady})

	res := h.Read(0, 0, []string{"agent.provisioned", "agent.started"})
	if len(res.Events) != 2 {
		t.Fatalf("got %d lifecycle events, want 2", len(res.Events))
	}
	for _, ev := range res.Events {
		if !strings.HasPrefix(ev.Type, "agent.") {
			t.Fatalf("filter leaked non-lifecycle event %q", ev.Type)
		}
	}
}

func TestReadLastReturnsTail(t *testing.T) {
	h := NewEventHub()
	for i := 1; i <= 10; i++ {
		h.Publish(json.RawMessage(fmt.Sprintf(`{"type":"record","n":%d}`, i)))
	}
	res := h.ReadLast(3, nil)
	if len(res.Events) != 3 || res.Events[0].Seq != 8 || res.Events[2].Seq != 10 {
		t.Fatalf("ReadLast(3) = seqs %v, want [8 9 10]", seqs(res.Events))
	}
}

// --- ring cap + truncated ---

func TestRingCapEvictsOldestAndSetsTruncated(t *testing.T) {
	h := newEventHub(10)
	for i := 1; i <= 25; i++ {
		h.Publish(json.RawMessage(fmt.Sprintf(`{"type":"record","n":%d}`, i)))
	}
	if res := h.Read(0, 100, nil); len(res.Events) != 10 || res.Events[0].Seq != 16 {
		t.Fatalf("buffer = seqs %v, want last 10 (16..25)", seqs(res.Events))
	}
	if !h.Read(0, 100, nil).Truncated {
		t.Fatal("truncated = false after eviction, want true")
	}
	// A hub that never evicted reports truncated: false.
	fresh := NewEventHub()
	fresh.Publish(json.RawMessage(`{"type":"record"}`))
	if fresh.Read(0, 100, nil).Truncated {
		t.Fatal("fresh hub reports truncated")
	}
}

// --- HTTP read_events contract ---

func TestReadEventsHTTPContract(t *testing.T) {
	h, _, _, agent := newTestAgentHandler(t)
	base := "/agents/" + agent.ID + "/events"

	// after + last together -> 400.
	code, body := doJSON(t, h, "GET", base+"?after=1&last=2", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("after+last status = %d, want 400 (%v)", code, body)
	}
	// Invalid cursor -> 400.
	if code, _ := doJSON(t, h, "GET", base+"?after=abc", nil); code != http.StatusBadRequest {
		t.Fatalf("after=abc status = %d, want 400", code)
	}
	if code, _ := doJSON(t, h, "GET", base+"?limit=-1", nil); code != http.StatusBadRequest {
		t.Fatalf("limit=-1 status = %d, want 400", code)
	}

	// after cursor + limit bound.
	code, body = doJSON(t, h, "GET", base+"?after=0&limit=2", nil)
	if code != http.StatusOK {
		t.Fatalf("read_events status = %d, want 200", code)
	}
	evs := body["events"].([]interface{})
	if len(evs) != 2 {
		t.Fatalf("limit=2 returned %d events", len(evs))
	}
	firstSeq := evs[0].(map[string]interface{})["seq"].(float64)
	if firstSeq != 1 {
		t.Fatalf("first seq = %v, want 1", firstSeq)
	}

	// types filter.
	code, body = doJSON(t, h, "GET", base+"?types=agent.provisioned,agent.started", nil)
	if code != http.StatusOK {
		t.Fatalf("types filter status = %d", code)
	}
	for _, e := range body["events"].([]interface{}) {
		typ := e.(map[string]interface{})["type"].(string)
		if !strings.HasPrefix(typ, "agent.") {
			t.Fatalf("types filter leaked %q", typ)
		}
	}

	// last=1 returns exactly the final event.
	_, body = doJSON(t, h, "GET", base+"?last=1", nil)
	evs = body["events"].([]interface{})
	if len(evs) != 1 {
		t.Fatalf("last=1 returned %d events", len(evs))
	}
	// truncated is present and false (nothing evicted).
	if body["truncated"] != false {
		t.Fatalf("truncated = %v, want false", body["truncated"])
	}
}

// --- SSE re-attach ---

func TestSSEReattachReplaysFromCursorThenLive(t *testing.T) {
	h, _, stream, agent := newTestAgentHandler(t)
	base := "/agents/" + agent.ID + "/events"

	// Turn one: two pi records.
	stream.Deliver(`{"type":"message_update","content":"one"}`)
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return len(agent.hub.Events()) >= 4 }, "records buffered")

	// Re-attach from cursor 2: only events after seq 2 replay.
	code, raw := doRaw(t, h, "GET", base, func(r *http.Request) { r.Header.Set("Last-Event-ID", "2") })
	if code != http.StatusOK {
		t.Fatalf("SSE status = %d", code)
	}
	assertSSEHas(t, raw, "3", `{"type":"message_update","content":"one"}`)
	assertSSELacks(t, raw, "id: 1\n", "id: 2\n")

	// Publish one more record (mid-turn continuation), then a full replay
	// from start must include it.
	stream.Deliver(`{"type":"message_update","content":"two"}`)
	waitFor(t, func() bool { return len(agent.hub.Events()) >= 5 }, "fifth event buffered")

	// Full replay from start when no cursor is given.
	code, raw = doRaw(t, h, "GET", base, func(r *http.Request) {})
	if code != http.StatusOK {
		t.Fatalf("SSE status = %d", code)
	}
	assertSSEHas(t, raw, "1", `"type":"agent.provisioned"`)
	assertSSEHas(t, raw, "5", `{"type":"message_update","content":"two"}`)
}

// doRaw issues a request against the handler with a cancellable context and
// returns the full streamed body.
func doRaw(t *testing.T, h http.Handler, method, target string, mutate func(*http.Request)) (int, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(method, target, nil).WithContext(ctx)
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()
	time.Sleep(150 * time.Millisecond) // replay + live window
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE handler did not return after cancel")
	}
	return rec.Code, rec.Body.String()
}

// assertSSEHas checks the body carries the id line and the data substring
// (matched as a substring of the framed record, not the full payload).
func assertSSEHas(t *testing.T, body, id, data string) {
	t.Helper()
	if !strings.Contains(body, "id: "+id+"\n") || !strings.Contains(body, data) {
		t.Fatalf("SSE body missing id %s / data %s\ngot:\n%s", id, data, body)
	}
}

func assertSSELacks(t *testing.T, body string, substrs ...string) {
	t.Helper()
	for _, s := range substrs {
		if strings.Contains(body, s) {
			t.Fatalf("SSE body should not contain %q\ngot:\n%s", s, body)
		}
	}
}

// --- helpers ---

func seqs(evs []Event) []uint64 {
	out := make([]uint64, len(evs))
	for i, ev := range evs {
		out[i] = ev.Seq
	}
	return out
}

// newTestAgentHandler wires the router over a fake-gateway manager and
// returns a ready agent (provisioned + started lifecycle events emitted).
func newTestAgentHandler(t *testing.T) (http.Handler, *AgentManager, *FakeStream, *Agent) {
	t.Helper()
	mgr, _, stream, agent := newTestAgent(t)
	return NewRouter(mgr), mgr, stream, agent
}

