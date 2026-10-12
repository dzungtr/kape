package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

type fixture struct {
	router http.Handler
	spool  *Spool
	pi     *Pi
}

func newFixture(t *testing.T, token string) *fixture {
	t.Helper()
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool.jsonl"), 100)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	t.Cleanup(func() { spool.Close() })
	pi := NewPi([]string{"sh", "-c", "sleep 30"}, spool)
	if err := pi.Start(); err != nil {
		t.Fatalf("start fake pi: %v", err)
	}
	f := &fixture{
		router: NewRouter("agent-1", "boot-abc", spool, pi, NewCommandQueue(), token),
		spool:  spool,
		pi:     pi,
	}
	return f
}

func (f *fixture) do(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// D5 placeholder: requests without a credential → 401; /healthz stays open.
func TestAuthPlaceholder(t *testing.T) {
	f := newFixture(t, "sekrit")
	for _, path := range []string{"/status", "/records?after=0", "/commands"} {
		if got := f.do(t, "GET", path, "", nil).Code; got != http.StatusUnauthorized {
			t.Errorf("%s without Authorization = %d, want 401", path, got)
		}
	}
	if got := f.do(t, "GET", "/records?after=0", "wrong", nil).Code; got != http.StatusUnauthorized {
		t.Errorf("/records with wrong token = %d, want 401", got)
	}
	if got := f.do(t, "GET", "/healthz", "", nil).Code; got != http.StatusOK {
		t.Errorf("/healthz without Authorization = %d, want 200", got)
	}
	if got := f.do(t, "GET", "/status", "sekrit", nil).Code; got != http.StatusOK {
		t.Errorf("/status with token = %d, want 200", got)
	}
}

func TestStatusShape(t *testing.T) {
	f := newFixture(t, "tok")
	rec := f.do(t, "GET", "/status", "tok", nil)
	var s Status
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if s.AgentID != "agent-1" || s.BootID != "boot-abc" {
		t.Fatalf("status = %+v", s)
	}
	if !s.PiRunning {
		t.Fatal("pi_running = false before Start, want true")
	}
	f.pi.Kill()
	deadline := time.Now().Add(time.Second)
	for f.pi.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.pi.Running() {
		t.Fatal("pi still running after Kill")
	}
}

func TestCommandsLifecycle(t *testing.T) {
	f := newFixture(t, "tok")
	rec := f.do(t, "POST", "/commands", "tok", Command{ID: "c1", Type: CommandPrompt, Payload: json.RawMessage(`"hello"`)})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /commands = %d, want 202", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/commands/c1" {
		t.Fatalf("Location = %q, want /commands/c1", loc)
	}
	var stored Command
	if err := json.Unmarshal(rec.Body.Bytes(), &stored); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stored.Status != "queued" {
		t.Fatalf("status = %q, want queued", stored.Status)
	}
	if rec := f.do(t, "GET", "/commands/c1", "tok", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /commands/c1 = %d, want 200", rec.Code)
	}
	if rec := f.do(t, "GET", "/commands/nope", "tok", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("GET unknown command = %d, want 404", rec.Code)
	}
	if rec := f.do(t, "POST", "/commands", "tok", Command{ID: "c2", Type: "restart"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid command type = %d, want 400", rec.Code)
	}
}

// /records serves the spool in ascending spool_id order with verbatim data.
func TestRecordsEndpoint(t *testing.T) {
	f := newFixture(t, "tok")
	f.spool.Append(`{"type":"turn_started","turn_id":"t-0001"}`)
	f.spool.Append(`{"type":"text_delta","text":"unfiltered delta"}`)
	rec := f.do(t, "GET", "/records?after=0", "tok", nil)
	var resp struct {
		Records []Record `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Records) != 2 {
		t.Fatalf("got %d records, want 2", len(resp.Records))
	}
	if resp.Records[0].SpoolID != 1 || resp.Records[1].SpoolID != 2 {
		t.Fatalf("spool_ids = %d,%d", resp.Records[0].SpoolID, resp.Records[1].SpoolID)
	}
	var delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(resp.Records[1].Data, &delta); err != nil || delta.Text != "unfiltered delta" {
		t.Fatalf("text_delta not served unfiltered: %v %s", err, resp.Records[1].Data)
	}
	rec = f.do(t, "GET", "/records?after=1", "tok", nil)
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Records) != 1 || resp.Records[0].SpoolID != 2 {
		t.Fatalf("after=1 got %d records, want 1 at spool_id 2", len(resp.Records))
	}
	if rec := f.do(t, "GET", "/records?after=-1", "tok", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad after = %d, want 400", rec.Code)
	}
}
