package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Fake pi emits the deterministic four-record turn per prompt, turn ids
// increasing (issue #210 AC: "fake pi emits a deterministic turn driven by
// prompt commands").
func TestFakePiDeterministicTurns(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"prompt","command_id":"c1","payload":{"message":"hello"}}`,
		`{"type":"prompt","command_id":"c2","payload":{"message":"again"}}`,
	}, "\n") + "\n"
	var out, stderr bytes.Buffer
	if err := runFakePi(strings.NewReader(in), &out, &stderr); err != nil {
		t.Fatalf("runFakePi: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	var types []string
	var turnIDs []string
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		var rec fakePiRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("record %q not JSON: %v", line, err)
		}
		types = append(types, rec.Type)
		turnIDs = append(turnIDs, rec.TurnID)
	}
	wantTypes := []string{"turn_started", "tool_call", "text_delta", "agent_settled", "turn_started", "tool_call", "text_delta", "agent_settled"}
	for i, want := range wantTypes {
		if types[i] != want {
			t.Fatalf("record %d type = %q, want %q", i, types[i], want)
		}
	}
	if turnIDs[0] != "t-0001" || turnIDs[4] != "t-0002" {
		t.Fatalf("turn ids not ordinal: %v", turnIDs)
	}

	// Determinism: same input → byte-identical output.
	var out2 bytes.Buffer
	runFakePi(strings.NewReader(in), &out2, &bytes.Buffer{}) //nolint:errcheck
	if out.String() != out2.String() {
		t.Fatal("fake pi output is not deterministic across runs")
	}
}

// Non-prompt commands drive the queue (issue #250): steer/follow_up enqueue
// and emit queue_update; abort settles without touching the queue (pi's abort
// alone continues queued messages — the reason Esc-abort is clear_queue THEN
// abort); clear_queue drains and reports the empty queue.
func TestFakePiQueueAndAbort(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"steer","payload":{"message":"add salt"}}`,
		`{"type":"follow_up","payload":{"message":"and pepper"}}`,
		`{"type":"abort"}`,
		`{"type":"clear_queue"}`,
	}, "\n") + "\n"
	var out, stderr bytes.Buffer
	if err := runFakePi(strings.NewReader(in), &out, &stderr); err != nil {
		t.Fatalf("runFakePi: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	var recs []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("record %q not JSON: %v", line, err)
		}
		recs = append(recs, m)
	}
	checkQueue := func(i int, steering, followUp any) {
		t.Helper()
		if i >= len(recs) {
			t.Fatalf("expected record %d, got %d records", i, len(recs))
		}
		if recs[i]["type"] != "queue_update" {
			t.Fatalf("record %d = %v, want queue_update", i, recs[i])
		}
		if got := recs[i]["steering"]; !sameElements(got, steering) {
			t.Fatalf("queue_update %d steering = %v, want %v", i, got, steering)
		}
		if got := recs[i]["followUp"]; !sameElements(got, followUp) {
			t.Fatalf("queue_update %d followUp = %v, want %v", i, got, followUp)
		}
	}
	// Each enqueued message appends to that queue half; queue_update carries
	// the queue's full state, not just the delta.
	checkQueue(0, []any{"add salt"}, nil)
	checkQueue(1, []any{"add salt"}, []any{"and pepper"})
	// Abort settles without clearing the queue: no queue_update around the
	// settle marker, and aborted = true (issue #246 real-pi vocabulary).
	if recs[2]["type"] != "agent_settled" || recs[2]["aborted"] != true {
		t.Fatalf("record 2 = %v, want agent_settled {aborted:true}", recs[2])
	}
	// clear_queue drains both halves and reports the now-empty queue.
	checkQueue(3, nil, nil)
}

// sameElements compares a decoded JSON value against an expected []any (or
// nil, meaning an absent/empty value is not required — empty vs missing both
// pass as nil).
func sameElements(got, want any) bool {
	gotArr, _ := got.([]any)
	wantArr, _ := want.([]any)
	if len(gotArr) != len(wantArr) {
		return false
	}
	for i := range gotArr {
		if gotArr[i] != wantArr[i] {
			return false
		}
	}
	return true
}
