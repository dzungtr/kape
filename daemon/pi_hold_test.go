package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// hold_ms paces the turn mid-flight so a mid-turn outage leaves records
// pending (failure matrix, issue #217): the turn must still complete, in
// order, after the hold.
func TestFakePiHoldMs(t *testing.T) {
	cmd, _ := json.Marshal(map[string]any{
		"type": "prompt",
		"payload": map[string]any{
			"message": `{"hold_ms":50}`,
		},
	})
	var out bytes.Buffer
	start := time.Now()
	if err := runFakePi(strings.NewReader(string(cmd)+"\n"), &out, &out); err != nil {
		t.Fatalf("runFakePi: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("hold_ms not honored: turn finished in %v", elapsed)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var types []string
	for _, l := range lines {
		var r fakePiRecord
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("record %q: %v", l, err)
		}
		types = append(types, r.Type)
	}
	want := []string{"turn_started", "tool_call", "text_delta", "agent_settled"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Errorf("records %v, want %v", types, want)
	}
}
