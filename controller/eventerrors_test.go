package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// failingStore is an EventStore whose reads fail, standing in for a corrupt
// or unreadable durable log.
type failingStore struct{}

func (failingStore) NextSeq(string) (uint64, error) { return 0, nil }
func (failingStore) Append(string, Event) error     { return nil }
func (failingStore) Replay(string, uint64) ([]Event, error) {
	return nil, errors.New("store read failed")
}
func (failingStore) Close() error { return nil }

func newFailingHub(t *testing.T) *EventHub {
	t.Helper()
	h, err := NewEventHub(failingStore{}, "agent-err")
	if err != nil {
		t.Fatalf("NewEventHub: %v", err)
	}
	return h
}

// F2: store read failures must surface to the read_events/stream_events
// response, not be swallowed after logging (silent empty replay).

func TestReadSurfacesStoreError(t *testing.T) {
	h := newFailingHub(t)
	if res := h.Read(0, 10, nil); res.Err == nil {
		t.Fatalf("Read on failing store: Err = nil, want error")
	}
	if res := h.ReadLast(3, nil); res.Err == nil {
		t.Fatalf("ReadLast on failing store: Err = nil, want error")
	}
	if _, _, _, err := h.Subscribe(0); err == nil {
		t.Fatalf("Subscribe on failing store: err = nil, want error")
	}
}

func TestReadEventsHTTPSurfacesStoreError(t *testing.T) {
	h, _, _, agent := newTestAgentHandler(t)
	agent.hub = newFailingHub(t)
	base := "/agents/" + agent.ID + "/events"
	code, body := doJSON(t, h, "GET", base+"?after=0", nil)
	if code != http.StatusInternalServerError {
		t.Fatalf("read_events status = %d, want 500; body %s", code, body)
	}
	if code, _ := doJSON(t, h, "GET", base+"?last=3", nil); code != http.StatusInternalServerError {
		t.Fatalf("read_events last status = %d, want 500", code)
	}
}

func TestSSESurfacesStoreError(t *testing.T) {
	h, _, _, agent := newTestAgentHandler(t)
	agent.hub = newFailingHub(t)
	req := httptest.NewRequest("GET", "/agents/"+agent.ID+"/events?after=0", nil)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("SSE status = %d, want 500", rec.Code)
	}
}

func TestMCPReadEventsSurfacesStoreError(t *testing.T) {
	_, mgr, _, agent := newTestAgentHandler(t)
	agent.hub = newFailingHub(t)

	read := handleReadEvents(mgr)
	_, _, err := read(context.Background(), nil, readEventsInput{AgentID: agent.ID})
	if err == nil {
		t.Fatalf("MCP read_events: err = nil, want store error")
	}

	stream := handleStreamEvents(mgr)
	_, _, err = stream(context.Background(), nil, streamEventsInput{AgentID: agent.ID})
	if err == nil {
		t.Fatalf("MCP stream_events: err = nil, want store error")
	}
}
