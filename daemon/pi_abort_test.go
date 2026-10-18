package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Esc-abort (spec #245 E6, issue #250): the front-door abort maps to pi
// clear_queue + abort, in that order — pi's abort alone continues queued
// messages (#246 findings, rpc-commands.md). The drained queue texts are
// spooled as a queue_drained notice (honest history); clear_queue itself
// stays an independent break-glass command (ADR-0083 D4).
//
// The scripted pi echoes every stdin line verbatim, so the echoed records
// double as an ordered capture of what reached pi's stdin.

// waitForSpool polls the spool until at least n records exist or the deadline
// expires.
func waitForSpool(t *testing.T, spool *Spool, n int) []Record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(spool.Read(0, 0)) < n && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	return spool.Read(0, 0)
}

// decodeNotice is the envelope for daemon notices under test.
type drainNotice struct {
	Type      string   `json:"type"`
	Steering  []string `json:"steering"`
	FollowUp  []string `json:"follow_up"`
	SteerSet  bool
	FollowSet bool
}

func decodeType(t *testing.T, data string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("record not JSON: %v (%s)", err, data)
	}
	return m
}

// Abort with an empty queue → the command pair clear_queue then abort reaches
// pi in that order, and no queue_drained notice is emitted (no spurious
// notice on an empty queue).
func TestPiAbortEmptyQueueCommandPair(t *testing.T) {
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool.jsonl"), 100)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	defer spool.Close() //nolint:errcheck
	pi := NewPi([]string{"sh", "-c", "while read line; do echo \"$line\"; done"}, spool, "", "")
	if err := pi.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	if err := pi.SendCommand(t.Context(), Command{ID: "a1", Type: CommandAbort}); err != nil {
		t.Fatalf("SendCommand abort: %v", err)
	}
	recs := waitForSpool(t, spool, 2)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want exactly 2 (clear_queue echo + abort echo)", len(recs))
	}
	wantTypes := []string{CommandClearQueue, CommandAbort}
	var ids []string
	for i, rec := range recs {
		got := decodeType(t, string(rec.Data))
		var typ string
		json.Unmarshal(got["type"], &typ) //nolint:errcheck
		if typ != wantTypes[i] {
			t.Fatalf("record %d type = %q, want %q (Esc ordering: clear_queue before abort)", i, typ, wantTypes[i])
		}
		var id string
		json.Unmarshal(got["id"], &id) //nolint:errcheck
		ids = append(ids, id)
	}
	// clear_queue carries the transport id; abort is a bare object (verified
	// against pi rpc-commands.md — no id field).
	if ids[0] != "a1" {
		t.Fatalf("clear_queue command id = %q, want a1", ids[0])
	}
	abort := string(recs[1].Data)
	if abort != `{"type":"abort"}` {
		t.Fatalf("abort line = %s, want bare object", abort)
	}
	for _, rec := range recs {
		if strings.Contains(string(rec.Data), "queue_drained") {
			t.Fatalf("spurious queue_drained notice on empty queue: %s", rec.Data)
		}
	}
}

// Abort with a queued steering message → the drained texts are spooled as a
// queue_drained notice before the abort reaches pi's stdin.
func TestPiAbortDrainsQueuedTextsAsNotice(t *testing.T) {
	script := `while read line; do echo "$line"
case "$line" in *'"steer"'*) echo '{"type":"queue_update","steering":["add salt"],"followUp":[]}';; esac
done`
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool.jsonl"), 100)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	defer spool.Close() //nolint:errcheck
	pi := NewPi([]string{"sh", "-c", script}, spool, "", "")
	if err := pi.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	if err := pi.SendCommand(t.Context(), Command{ID: "s1", Type: CommandSteer, Payload: json.RawMessage(`{"message":"add salt"}`)}); err != nil {
		t.Fatalf("SendCommand steer: %v", err)
	}
	waitForSpool(t, spool, 2) // steer echo + queue_update
	if err := pi.SendCommand(t.Context(), Command{ID: "a1", Type: CommandAbort}); err != nil {
		t.Fatalf("SendCommand abort: %v", err)
	}
	recs := waitForSpool(t, spool, 5) // steer echo, queue_update, notice, clear_queue echo, abort echo
	// Echo capture: {type, id} pairs of stdin-written records.
	type echo struct {
		typ, id string
		at      int
	}
	var noticeAt = -1
	var echoes []echo
	for i, rec := range recs {
		m := decodeType(t, string(rec.Data))
		var typ string
		json.Unmarshal(m["type"], &typ) //nolint:errcheck
		switch typ {
		case "queue_drained":
			if noticeAt != -1 {
				t.Fatalf("multiple queue_drained notices: %v", recs)
			}
			noticeAt = i
			var n drainNotice
			if err := json.Unmarshal(rec.Data, &n); err != nil {
				t.Fatalf("decode notice: %v", err)
			}
			if len(n.Steering) != 1 || n.Steering[0] != "add salt" {
				t.Fatalf("notice steering = %v, want [add salt]", n.Steering)
			}
			if len(n.FollowUp) != 0 {
				t.Fatalf("notice follow_up = %v, want empty", n.FollowUp)
			}
		case CommandSteer, CommandClearQueue, CommandAbort:
			var id string
			json.Unmarshal(m["id"], &id) //nolint:errcheck
			echoes = append(echoes, echo{typ, id, i})
		}
	}
	if noticeAt == -1 {
		t.Fatalf("no queue_drained notice in %v", recs)
	}
	// The pair must be clear_queue then abort (Esc ordering). Spool ordering
	// between the notice and the async clear_queue echo is not pinned: the
	// notice is spooled after the clear_queue stdin write (fix round 1
	// spool-guard) while the echo races back over the stdout relay.
	if len(echoes) < 3 {
		t.Fatalf("stdin echoes = %v, want steer + clear_queue + abort", echoes)
	}
	last := echoes[len(echoes)-2:]
	if last[0].typ != CommandClearQueue || last[1].typ != CommandAbort || last[0].id != "a1" || last[1].id != "" {
		t.Fatalf("trailing stdin echoes = %+v, want clear_queue(a1) then bare abort", last)
	}
	// The abort echo must not precede the notice: the notice is spooled under
	// p.mu between the clear_queue and abort stdin writes.
	if noticeAt > last[1].at {
		t.Fatalf("queue_drained notice at %d after abort echo at %d", noticeAt, last[1].at)
	}
}

// Fix round 1 (issue #250): a steer immediately followed by an abort must not
// lose the steered text when pi's queue_update has not been relayed yet —
// the optimistic tracker entry covers the relay gap.
func TestPiAbortNoticesSteerBeforeQueueUpdateRelayed(t *testing.T) {
	script := `while read line; do echo "$line"
case "$line" in *'"steer"'*) sleep 0.3; echo '{"type":"queue_update","steering":["add salt"],"followUp":[]}';; esac
done`
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool.jsonl"), 100)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	defer spool.Close() //nolint:errcheck
	pi := NewPi([]string{"sh", "-c", script}, spool, "", "")
	if err := pi.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	if err := pi.SendCommand(t.Context(), Command{ID: "s1", Type: CommandSteer, Payload: json.RawMessage(`{"message":"add salt"}`)}); err != nil {
		t.Fatalf("SendCommand steer: %v", err)
	}
	// No wait for the queue_update: abort lands in the relay gap.
	if err := pi.SendCommand(t.Context(), Command{ID: "a1", Type: CommandAbort}); err != nil {
		t.Fatalf("SendCommand abort: %v", err)
	}
	recs := waitForSpool(t, spool, 5) // steer echo, clear_queue echo, abort echo, notice, queue_update
	drains := 0
	for _, rec := range recs {
		if !strings.Contains(string(rec.Data), "queue_drained") {
			continue
		}
		drains++
		var n drainNotice
		if err := json.Unmarshal(rec.Data, &n); err != nil {
			t.Fatalf("decode notice: %v", err)
		}
		if len(n.Steering) != 1 || n.Steering[0] != "add salt" {
			t.Fatalf("notice steering = %v, want [add salt] (relay-gap text must not be lost)", n.Steering)
		}
	}
	if drains != 1 {
		t.Fatalf("queue_drained notices = %d, want exactly 1 in %v", drains, recs)
	}
}

// Fix round 1 (issue #250, ADR-0083 D4): a standalone clear_queue is
// forwarded as-is — exactly one line on pi's stdin, no queue_drained notice,
// no abort pairing.
func TestPiStandaloneClearQueueForwardedUnpaired(t *testing.T) {
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool.jsonl"), 100)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	defer spool.Close() //nolint:errcheck
	pi := NewPi([]string{"sh", "-c", "while read line; do echo \"$line\"; done"}, spool, "", "")
	if err := pi.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	if err := pi.SendCommand(t.Context(), Command{ID: "cq1", Type: CommandClearQueue}); err != nil {
		t.Fatalf("SendCommand clear_queue: %v", err)
	}
	recs := waitForSpool(t, spool, 1)
	time.Sleep(100 * time.Millisecond) // allow any wrongly emitted notice to land
	recs = spool.Read(0, 0)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want exactly 1 (the clear_queue echo): %v", len(recs), recs)
	}
	got := decodeType(t, string(recs[0].Data))
	var typ, id string
	json.Unmarshal(got["type"], &typ)      //nolint:errcheck
	json.Unmarshal(got["id"], &id) //nolint:errcheck
	if typ != CommandClearQueue || id != "cq1" {
		t.Fatalf("echo = %s, want clear_queue with id cq1", recs[0].Data)
	}
	if strings.Contains(string(recs[0].Data), "queue_drained") {
		t.Fatalf("standalone clear_queue must not emit a notice: %s", recs[0].Data)
	}
}

// The tracker follows queue updates: after a drain, a second abort must not
// re-notice the already-drained texts; texts are spooled exactly once.
func TestPiAbortNoticeNotRepeatedAfterDrain(t *testing.T) {
	script := `while read line; do echo "$line"
case "$line" in *'"steer"'*) echo '{"type":"queue_update","steering":["add salt"],"followUp":["later then"]}';; esac
done`
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool.jsonl"), 100)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	defer spool.Close() //nolint:errcheck
	pi := NewPi([]string{"sh", "-c", script}, spool, "", "")
	if err := pi.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	if err := pi.SendCommand(t.Context(), Command{ID: "s1", Type: CommandSteer, Payload: json.RawMessage(`{"message":"add salt"}`)}); err != nil {
		t.Fatalf("SendCommand steer: %v", err)
	}
	waitForSpool(t, spool, 2)
	if err := pi.SendCommand(t.Context(), Command{ID: "a1", Type: CommandAbort}); err != nil {
		t.Fatalf("first abort: %v", err)
	}
	waitForSpool(t, spool, 5)
	if err := pi.SendCommand(t.Context(), Command{ID: "a2", Type: CommandAbort}); err != nil {
		t.Fatalf("second abort: %v", err)
	}
	recs := waitForSpool(t, spool, 7) // first 5 + clear_queue echo + abort echo
	drains := 0
	for _, rec := range recs {
		if strings.Contains(string(rec.Data), "queue_drained") {
			drains++
		}
	}
	if drains != 1 {
		t.Fatalf("queue_drained notices = %d, want exactly 1 across two aborts", drains)
	}
}
