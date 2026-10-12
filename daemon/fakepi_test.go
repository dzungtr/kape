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
	runFakePi(strings.NewReader(in), &out2, &bytes.Buffer{})
	if out.String() != out2.String() {
		t.Fatal("fake pi output is not deterministic across runs")
	}

	// Steer/abort/clear_queue are accepted and emit nothing in the stub.
	var quiet bytes.Buffer
	if err := runFakePi(strings.NewReader(`{"type":"steer"}`+"\n"+`{"type":"abort"}`+"\n"+`{"type":"clear_queue"}`+"\n"), &quiet, &bytes.Buffer{}); err != nil {
		t.Fatalf("runFakePi quiet: %v", err)
	}
	if quiet.Len() != 0 {
		t.Fatalf("non-prompt commands emitted records: %s", quiet.String())
	}
}
