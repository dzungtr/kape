package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// End-to-end through the Pi subprocess manager with a scripted fake pi
// (stand-in for the built-in fakepi binary): stdout spooled verbatim, stderr
// becomes pi_stderr notices, exit becomes pi_exit, pi_running tracks liveness.
func TestPiRelayAndNotices(t *testing.T) {
	script := `echo '{"type":"turn_started","turn_id":"t-0001"}'; echo 'not json garbage' >&2; exit 3`
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool.jsonl"), 100)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	defer spool.Close() //nolint:errcheck
	pi := NewPi([]string{"sh", "-c", script}, spool)
	if err := pi.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for (pi.Running() || len(spool.Read(0, 0)) < 3) && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if pi.Running() {
		t.Fatal("pi did not exit")
	}
	recs := spool.Read(0, 0)
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3 (stdout + pi_stderr + pi_exit)", len(recs))
	}
	var record map[string]any
	if err := json.Unmarshal(recs[0].Data, &record); err != nil {
		t.Fatalf("pi stdout re-wrapped: %v (%s)", err, recs[0].Data)
	}
	if record["type"] != "turn_started" {
		t.Fatalf("first record = %s, want verbatim turn_started", recs[0].Data)
	}
	var notice struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Code *int   `json:"exit_code"`
	}
	if err := json.Unmarshal(recs[1].Data, &notice); err != nil || notice.Type != "pi_stderr" || notice.Text == "" {
		t.Fatalf("second record = %s, want pi_stderr with text", recs[1].Data)
	}
	if err := json.Unmarshal(recs[2].Data, &notice); err != nil || notice.Type != "pi_exit" || notice.Code == nil || *notice.Code != 3 {
		t.Fatalf("third record = %s, want pi_exit with code 3", recs[2].Data)
	}
}

// A prompt command reaches pi's stdin as one JSON line; a prompt on a dead pi
// silently respawns it (pi_restart notice reserved for Epic 3).
func TestPiPromptAndRespawn(t *testing.T) {
	script := `while read line; do echo "$line"; done`
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool.jsonl"), 100)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	defer spool.Close() //nolint:errcheck
	pi := NewPi([]string{"sh", "-c", script}, spool)
	if err := pi.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	c1 := Command{ID: "c1", Type: CommandPrompt, Payload: json.RawMessage(`{"message":"hi"}`)}
	if err := pi.SendCommand(t.Context(), c1); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	waitFor := func(n int) []Record {
		deadline := time.Now().Add(5 * time.Second)
		for len(spool.Read(0, 0)) < n && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		return spool.Read(0, 0)
	}
	if recs := waitFor(1); len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	} else {
		var record struct {
			CommandID string          `json:"command_id"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(recs[0].Data, &record); err != nil {
			t.Fatalf("decode echo: %v (%s)", err, recs[0].Data)
		}
		if record.CommandID != "c1" || record.Type != "prompt" || string(record.Payload) != `{"message":"hi"}` {
			t.Fatalf("pi did not receive the command verbatim: %s", recs[0].Data)
		}
	}

	pi.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for pi.Running() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if pi.Running() {
		t.Fatal("pi survived Kill")
	}
	c2 := Command{ID: "c2", Type: CommandPrompt, Payload: json.RawMessage(`{"message":"hi"}`)}
	if err := pi.SendCommand(t.Context(), c2); err != nil {
		t.Fatalf("SendCommand after kill: %v", err)
	}
	recs := waitFor(3) // c1 echo, pi_exit, c2 echo
	if len(recs) < 3 {
		t.Fatalf("got %d records, want >= 3 after respawn", len(recs))
	}
	var record struct {
		CommandID string `json:"command_id"`
	}
	if err := json.Unmarshal(recs[2].Data, &record); err != nil || record.CommandID != "c2" {
		t.Fatalf("respawned pi record = %s, want c2 echo", recs[2].Data)
	}
}

// Regression: a stale generation's awaitExit must not clear pi_running after a
// respawn. If the old process's Wait() returns after SendCommand has already
// spawned the next generation, the unguarded Store(false) would wedge
// pi_running false forever (ADR-0083 D6 reconnect preflight).
func TestPiStaleExitDoesNotClobberRunning(t *testing.T) {
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool.jsonl"), 100)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	defer spool.Close() //nolint:errcheck
	pi := NewPi([]string{"sh", "-c", "true"}, spool)
	if err := pi.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	pi.mu.Lock()
	oldCmd := pi.cmd
	pi.mu.Unlock()

	// Let generation 1 die and its real awaitExit complete.
	deadline := time.Now().Add(5 * time.Second)
	for pi.Running() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if pi.Running() {
		t.Fatal("pi did not exit")
	}

	// Respawn (generation 2), then simulate the stale waiter firing late.
	if err := pi.Start(); err != nil {
		t.Fatalf("respawn: %v", err)
	}
	if !pi.Running() {
		t.Fatal("respawned pi reports not running before stale exit fires")
	}
	pi.awaitExit(oldCmd) // stale waiter: Wait on an already-reaped process
	if !pi.Running() {
		t.Fatal("stale awaitExit clobbered pi_running for the live generation")
	}
}
