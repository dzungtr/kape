package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustSpool(t *testing.T, path string, max int) *Spool {
	t.Helper()
	s, err := NewSpool(path, max)
	if err != nil {
		t.Fatalf("NewSpool: %v", err)
	}
	t.Cleanup(func() { s.Close() }) //nolint:errcheck
	return s
}

// Spool ids are monotonic and durable: a reopen continues from the stored tail
// with no reset and no reuse (issue #210 AC, ADR-0083 D6 recover-from-file).
func TestSpoolSeqContinuesAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.jsonl")
	s1 := mustSpool(t, path, 100)
	r1, err := s1.Append(`{"type":"turn_started","turn_id":"t-0001"}`)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := s1.Append(`{"type":"agent_settled"}`); err != nil {
		t.Fatalf("append: %v", err)
	}
	if r1.SpoolID != 1 {
		t.Fatalf("first spool_id = %d, want 1", r1.SpoolID)
	}
	_ = s1.Close() // hard close like a daemon restart

	s2 := mustSpool(t, path, 100)
	r3, err := s2.Append(`{"type":"text_delta","text":"after restart"}`)
	if err != nil {
		t.Fatalf("append after restart: %v", err)
	}
	if r3.SpoolID != 3 {
		t.Fatalf("spool_id after restart = %d, want 3 (no reset, no reuse)", r3.SpoolID)
	}
}

// /records long-poll: returns immediately on arrival, empty at wait timeout.
func TestSpoolReadLongPoll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.jsonl")
	s := mustSpool(t, path, 100)
	s.Append(`{"a":1}`) //nolint:errcheck
	s.Append(`{"a":2}`) //nolint:errcheck

	if got := len(s.Read(0, 0)); got != 2 {
		t.Fatalf("replay got %d records, want 2", got)
	}
	if got := len(s.Read(2, 0)); got != 0 {
		t.Fatalf("at-tail immediate read got %d records, want 0", got)
	}

	// Timeout path: empty return after the wait.
	start := time.Now()
	got := s.Read(2, 50*time.Millisecond)
	if len(got) != 0 {
		t.Fatalf("timed-out read got %d records, want 0", len(got))
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("read returned before wait expired")
	}

	// Arrival path: a blocking read returns as soon as a record lands.
	done := make(chan []Record, 1)
	go func() { done <- s.Read(2, 5*time.Second) }()
	time.Sleep(10 * time.Millisecond)
	s.Append(`{"a":3}`) //nolint:errcheck
	select {
	case recs := <-done:
		if len(recs) != 1 || recs[0].SpoolID != 3 {
			t.Fatalf("long-poll arrival got %+v, want spool_id 3", recs)
		}
		var body map[string]any
		if err := json.Unmarshal(recs[0].Data, &body); err != nil {
			t.Fatalf("record data not verbatim JSON: %v", err)
		}
		if body["a"].(float64) != 3 {
			t.Fatalf("record data = %s", recs[0].Data)
		}
	case <-time.After(time.Second):
		t.Fatal("long-poll did not return on arrival")
	}
}

// A >1MiB record in the spool file must not boot-loop the daemon: recovery
// scanning is unbounded (bufio.Reader, not Scanner), and spool_id continues.
func TestSpoolOversizedRecordRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.jsonl")
	big := fmt.Sprintf(`{"type":"text_delta","text":"%s"}`, strings.Repeat("x", 1<<20+512))
	env, err := json.Marshal(spoolEntry{SpoolID: 7, Data: big})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := os.WriteFile(path, append(env, '\n'), 0o600); err != nil {
		t.Fatalf("write spool: %v", err)
	}

	s := mustSpool(t, path, 100)
	recs := s.Read(0, 0)
	if len(recs) != 1 || recs[0].SpoolID != 7 || len(recs[0].Data) <= 1<<20 {
		t.Fatalf("recovered records = %d (first id %d, len %d), want the full oversized record", len(recs), recs[0].SpoolID, len(recs[0].Data))
	}
	r, err := s.Append(`{"type":"agent_settled"}`)
	if err != nil {
		t.Fatalf("append after recovery: %v", err)
	}
	if r.SpoolID != 8 {
		t.Fatalf("spool_id after oversized record = %d, want 8 (continues)", r.SpoolID)
	}
}

// A torn final line (crash between Write and Sync) must not fail recovery:
// the daemon resyncs to the last newline and discards the fragment.
func TestSpoolTornFinalLineRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.jsonl")
	good1, _ := json.Marshal(spoolEntry{SpoolID: 1, Data: `{"a":1}`})
	good2, _ := json.Marshal(spoolEntry{SpoolID: 2, Data: `{"a":2}`})
	torn, _ := json.Marshal(spoolEntry{SpoolID: 3, Data: `{"a":3}`})
	body := string(good1) + "\n" + string(good2) + "\n" + string(torn[:len(torn)/2]) // no trailing newline
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write spool: %v", err)
	}

	s := mustSpool(t, path, 100)
	recs := s.Read(0, 0)
	if len(recs) != 2 || recs[1].SpoolID != 2 {
		t.Fatalf("recovered %d records, want the 2 complete ones (torn fragment discarded)", len(recs))
	}
	r, err := s.Append(`{"a":4}`)
	if err != nil {
		t.Fatalf("append after recovery: %v", err)
	}
	if r.SpoolID != 3 {
		t.Fatalf("spool_id after torn line = %d, want 3 (no reuse of the torn id)", r.SpoolID)
	}
}

// Beyond the cap the oldest records drop and a spool_truncated notice lands.
func TestSpoolTruncationEmitsNotice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spool.jsonl")
	s := mustSpool(t, path, 3)
	for i := 0; i < 4; i++ {
		if _, err := s.Append(`{"i":` + string(rune('0'+i)) + `}`); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	recs := s.Read(0, 0)
	if len(recs) != 4 {
		t.Fatalf("got %d records, want 3 kept + 1 notice", len(recs))
	}
	var notice struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(recs[3].Data, &notice); err != nil {
		t.Fatalf("notice data: %v", err)
	}
	if notice.Type != "spool_truncated" {
		t.Fatalf("notice type = %q, want spool_truncated", notice.Type)
	}
	if len(recs[0].Data) == 0 || recs[0].SpoolID == 0 {
		t.Fatalf("oldest record was not dropped: %+v", recs[0])
	}
}
