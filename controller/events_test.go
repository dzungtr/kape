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
	h := testHub(t)
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
	h := testHub(t)
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
	h := testHub(t)
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
	h := testHub(t)
	for i := 1; i <= 10; i++ {
		h.Publish(json.RawMessage(fmt.Sprintf(`{"type":"record","n":%d}`, i)))
	}
	res := h.ReadLast(3, nil)
	if len(res.Events) != 3 || res.Events[0].Seq != 8 || res.Events[2].Seq != 10 {
		t.Fatalf("ReadLast(3) = seqs %v, want [8 9 10]", seqs(res.Events))
	}
}

// --- durability: restart, replay-after-cursor, seq continuity ---

// testStore opens a BoltEventStore in the test's temp dir.
func testStore(t *testing.T) *BoltEventStore {
	t.Helper()
	s, err := NewBoltEventStore(t.TempDir() + "/events.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// testHub returns a hub over a fresh durable store for one agent.
func testHub(t *testing.T) *EventHub {
	t.Helper()
	h, err := NewEventHub(testStore(t), "agent-test")
	if err != nil {
		t.Fatalf("NewEventHub: %v", err)
	}
	return h
}

// TestHistoryAndCursorsSurviveRestart pins the #195 acceptance criterion:
// closing and reopening the store (a controller restart) keeps the full
// history replayable and the per-agent seq continuing without gaps or reuse.
func TestHistoryAndCursorsSurviveRestart(t *testing.T) {
	store, path := func() (*BoltEventStore, string) {
		dir := t.TempDir()
		s, err := NewBoltEventStore(dir + "/events.db")
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		return s, dir + "/events.db"
	}()

	h, err := NewEventHub(store, "agent-x")
	if err != nil {
		t.Fatalf("NewEventHub: %v", err)
	}
	h.PublishLifecycle("agent.started", map[string]interface{}{"agent_id": "agent-x", "status": StatusReady})
	h.Publish(json.RawMessage(`{"type":"agent_settled"}`))
	if got := h.Publish(json.RawMessage(`{"type":"response"}`)); got != 3 {
		t.Fatalf("seq = %d, want 3", got)
	}

	// Simulate an unclean-then-clean restart: close the store entirely.
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := NewBoltEventStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })

	// Replay after cursor 1 is identical to the same query before restart.
	h2, err := NewEventHub(reopened, "agent-x")
	if err != nil {
		t.Fatalf("NewEventHub after restart: %v", err)
	}
	after := h2.Read(1, 0, nil).Events
	if len(after) != 2 {
		t.Fatalf("replay after=1 across restart: %v, want seqs [2 3]", seqs(after))
	}
	for i, want := range []struct {
		seq  uint64
		data string
	}{
		{2, `{"type":"agent_settled"}`},
		{3, `{"type":"response"}`},
	} {
		if after[i].Seq != want.seq || string(after[i].Data) != want.data {
			t.Fatalf("replay[%d] = %d %s, want %d %s", i, after[i].Seq, after[i].Data, want.seq, want.data)
		}
	}
	// Seq continues at 4 — no reuse, no gap.
	if got := h2.Publish(json.RawMessage(`{"type":"response"}`)); got != 4 {
		t.Fatalf("seq after restart = %d, want 4", got)
	}
}

// TestAppendRejectsSeqGap guards the no-gap invariant at the store boundary:
// an append that does not advance the log by one is rejected, so a
// miscounting writer can never create a hole or reuse a seq.
func TestAppendRejectsSeqGap(t *testing.T) {
	store := testStore(t)
	if err := store.Append("agent-g", Event{Seq: 2, Data: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("first append at seq 2 accepted, want rejection")
	}
	if err := store.Append("agent-g", Event{Seq: 1, Data: json.RawMessage(`{"type":"a"}`)}); err != nil {
		t.Fatalf("append seq 1: %v", err)
	}
	if err := store.Append("agent-g", Event{Seq: 1, Data: json.RawMessage(`{"type":"b"}`)}); err == nil {
		t.Fatal("reused seq 1 accepted, want rejection")
	}
	if err := store.Append("agent-g", Event{Seq: 3, Data: json.RawMessage(`{"type":"c"}`)}); err == nil {
		t.Fatal("gap seq 3 accepted, want rejection")
	}
	evs, err := store.Replay("agent-g", 0)
	if err != nil || len(evs) != 1 || evs[0].Seq != 1 {
		t.Fatalf("log = %v, err %v; want exactly seq 1", evs, err)
	}
}

// TestReplayIsAscendingPerAgent isolates the store: two agents never share
// a seq space, and replay is ordered by seq within each.
func TestReplayIsAscendingPerAgent(t *testing.T) {
	store := testStore(t)
	ha, _ := NewEventHub(store, "agent-a")
	hb, _ := NewEventHub(store, "agent-b")
	ha.Publish(json.RawMessage(`{"type":"r"}`))
	hb.Publish(json.RawMessage(`{"type":"r"}`))
	ha.Publish(json.RawMessage(`{"type":"r"}`))

	evs, err := store.Replay("agent-a", 0)
	if err != nil || len(evs) != 2 {
		t.Fatalf("agent-a replay = %v, err %v", evs, err)
	}
	if evs[0].Seq != 1 || evs[1].Seq != 2 {
		t.Fatalf("agent-a seqs %v, want ascending [1 2]", seqs(evs))
	}
	evs, _ = store.Replay("agent-b", 0)
	if len(evs) != 1 || evs[0].Seq != 1 {
		t.Fatalf("agent-b seqs %v, want [1]", seqs(evs))
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
	// limit=0 would be an unbounded batch — 400, per "bounded batches".
	if code, _ := doJSON(t, h, "GET", base+"?limit=0", nil); code != http.StatusBadRequest {
		t.Fatalf("limit=0 status = %d, want 400", code)
	}
	// last + limit together is a conflicting param set — 400.
	if code, _ := doJSON(t, h, "GET", base+"?last=2&limit=3", nil); code != http.StatusBadRequest {
		t.Fatalf("last+limit status = %d, want 400", code)
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

	// Empty poll (cursor at latest) serializes events as [], never null.
	code, body = doJSON(t, h, "GET", base+fmt.Sprintf("?after=%d", len(agent.hub.Events())), nil)
	if code != http.StatusOK {
		t.Fatalf("empty poll status = %d", code)
	}
	evs, ok := body["events"].([]interface{})
	if !ok {
		t.Fatalf("empty poll events = %v (%T), want []", body["events"], body["events"])
	}
	if len(evs) != 0 {
		t.Fatalf("empty poll returned %d events", len(evs))
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

// TestSSEReattachViaAfterReplayThenLiveOnSingleConnection pins the #172
// handoff: a re-attach carrying the after cursor (Accept: text/event-stream)
// is served as SSE — replaying events after the cursor, then delivering
// subsequent live events on the same connection with no gap and no duplicate.
func TestSSEReattachViaAfterReplayThenLiveOnSingleConnection(t *testing.T) {
	h, _, stream, agent := newTestAgentHandler(t)
	base := "/agents/" + agent.ID + "/events"

	// Turn so far: two pi records (seqs 3 and 4 after the lifecycle pair).
	stream.Deliver(`{"type":"message_update","content":"one"}`)
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return len(agent.hub.Events()) >= 4 }, "records buffered")

	// One connection, re-attached via after=2 as a stream request.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", base+"?after=2", nil).WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()

	// The replay frames (seqs 3 and 4 only) arrive first.
	waitFor(t, func() bool {
		return strings.Contains(rec.Body.String(), "id: 4\n")
	}, "replay frames flushed")
	replayBody := rec.Body.String()
	assertSSEHas(t, replayBody, "3", `"content":"one"`)
	assertSSELacks(t, replayBody, "id: 1\n", "id: 2\n")

	// A mid-connection publish must arrive live on the same response body.
	stream.Deliver(`{"type":"message_update","content":"two"}`)
	waitFor(t, func() bool {
		return strings.Contains(rec.Body.String(), "id: 5\n")
	}, "live frame after replay")
	full := rec.Body.String()
	assertSSEHas(t, full, "5", `"content":"two"`)
	if strings.Contains(full, "id: 1\n") || strings.Contains(full, "id: 2\n") {
		t.Fatalf("re-attach replayed pre-cursor events\ngot:\n%s", full)
	}
	if strings.Contains(full, "id: 4\n\nid: 4\n") || strings.Count(full, "id: 4\n") != 1 {
		t.Fatalf("duplicate seq 4 on re-attach\ngot:\n%s", full)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE handler did not return after cancel")
	}
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
	return NewRouter(mgr, ""), mgr, stream, agent
}
