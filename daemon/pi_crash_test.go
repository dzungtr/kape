package main

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is an injectable Pi clock for backoff tests: sleep advances the
// clock (the real semantics the backoff guard relies on) and records the
// durations the daemon chose to wait.
type fakeClock struct {
	mu     sync.Mutex
	nanos  atomic.Int64
	sleeps []time.Duration
}

func (c *fakeClock) now() time.Time { return time.Unix(0, c.nanos.Load()) }

func (c *fakeClock) advance(d time.Duration) { c.nanos.Add(int64(d)) }

func (c *fakeClock) sleep(d time.Duration) {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.mu.Unlock()
	c.nanos.Add(int64(d))
}

func (c *fakeClock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// newCrashPi builds a Pi over a scriptable sh command with the fake clock
// installed (default E4 policy: 5s rapid window, 1s→30s backoff, 60s reset).
func newCrashPi(t *testing.T, script string) (*Pi, *Spool, *fakeClock) {
	t.Helper()
	spool, err := NewSpool(filepath.Join(t.TempDir(), "spool.jsonl"), 100)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	t.Cleanup(func() { spool.Close() }) //nolint:errcheck
	pi := NewPi([]string{"sh", "-c", script}, spool, "", "")
	clk := &fakeClock{}
	pi.now = clk.now
	pi.sleep = clk.sleep
	return pi, spool, clk
}

// waitDead blocks until pi reports not running (the exit notice is already
// spooled by then) or the deadline expires.
func waitDead(t *testing.T, pi *Pi) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for pi.Running() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if pi.Running() {
		t.Fatal("pi did not exit")
	}
}

// crash simulates an external crash (SIGKILL from outside the daemon — not
// the daemon's own Kill, which is an intentional exit): it must count as an
// unexpected exit for backoff and pi_restart accounting.
func crash(pi *Pi) {
	pi.mu.Lock()
	cmd := pi.cmd
	pi.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// noticeTypes extracts {type, exit_code, backoff_ms} of each spool record.
func noticeTypes(t *testing.T, spool *Spool) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range spool.Read(0, 0) {
		var v map[string]any
		if json.Unmarshal(r.Data, &v) == nil {
			out = append(out, v)
		}
	}
	return out
}

// E4 crash-loop guard: consecutive rapid exits (<5s uptime) back the respawn
// off exponentially 1s→30s (capped); the daemon never stops respawning.
func TestRespawnBackoffRapidExits(t *testing.T) {
	pi, _, clk := newCrashPi(t, `exit 7`)
	if err := pi.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	cycle := func() {
		crash(pi)
		waitDead(t, pi)
		if err := pi.Start(); err != nil { // lazy respawn: the next command's Start
			t.Fatalf("respawn: %v", err)
		}
	}
	for i := 0; i < 7; i++ {
		cycle()
	}
	// Seven rapid exits → six exponential respawns + one capped respawn.
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	if got := clk.recorded(); len(got) != len(want) {
		t.Fatalf("backoff sleeps %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("backoff sleep %d = %v, want %v (all: %v)", i, got[i], want[i], got)
			}
		}
	}
	if b := pi.Backoff(); b != 30*time.Second {
		t.Fatalf("backoff = %v, want capped 30s", b)
	}
	if r := pi.Restarts(); r != 7 {
		t.Fatalf("pi_restarts = %d, want 7", r)
	}
}

// E4: a stable run ≥60s resets the crash-loop guard entirely — the next
// rapid exit restarts the backoff at the initial 1s, not where it left off.
func TestRespawnBackoffStableRunResets(t *testing.T) {
	pi, _, clk := newCrashPi(t, `exit 7`)
	if err := pi.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	crash(pi)
	waitDead(t, pi)
	if err := pi.Start(); err != nil {
		t.Fatalf("respawn: %v", err)
	}
	if got := clk.recorded(); len(got) != 1 || got[0] != time.Second {
		t.Fatalf("first respawn sleeps %v, want [1s]", got)
	}
	// A stable run: advance the fake clock past the reset threshold, then
	// crash. The guard must reset instead of stepping to 2s.
	clk.advance(61 * time.Second)
	crash(pi)
	waitDead(t, pi)
	if b := pi.Backoff(); b != 0 {
		t.Fatalf("backoff after stable run = %v, want 0", b)
	}
	if err := pi.Start(); err != nil {
		t.Fatalf("respawn after reset: %v", err)
	}
	if got := clk.recorded(); len(got) != 1 {
		t.Fatalf("respawn after reset slept %v, want no sleep", got)
	}
	// The guard restarts from the initial backoff on the next rapid exit.
	crash(pi)
	waitDead(t, pi)
	if b := pi.Backoff(); b != time.Second {
		t.Fatalf("backoff after post-reset rapid exit = %v, want 1s", b)
	}
}

// E4: pi_restart fires on every respawn-after-unexpected-exit, carrying the
// replaced generation's exit code + the backoff state; an intentional exit
// (daemon Kill) respawns silently.
func TestPiRestartNotice(t *testing.T) {
	pi, spool, _ := newCrashPi(t, `exit 7`)
	if err := pi.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitDead(t, pi) // the script exits 7 on its own: an unexpected exit
	if err := pi.Start(); err != nil {
		t.Fatalf("respawn: %v", err)
	}
	var notices []map[string]any
	for _, v := range noticeTypes(t, spool) {
		if typ, _ := v["type"].(string); typ == "pi_exit" || typ == "pi_restart" {
			notices = append(notices, v)
		}
	}
	if len(notices) != 2 {
		t.Fatalf("notices %v, want pi_exit + pi_restart", notices)
	}
	if notices[0]["type"] != "pi_exit" || notices[0]["exit_code"].(float64) != 7 {
		t.Fatalf("first notice = %v, want pi_exit code 7", notices[0])
	}
	if notices[1]["type"] != "pi_restart" ||
		notices[1]["exit_code"].(float64) != 7 ||
		notices[1]["backoff_ms"].(float64) != 1000 ||
		notices[1]["restarts"].(float64) != 1 {
		t.Fatalf("second notice = %v, want pi_restart exit_code 7 backoff_ms 1000 restarts 1", notices[1])
	}
	// Intentional exit (daemon Kill, e.g. abort): respawn must NOT emit
	// pi_restart and must not sleep.
	pi.Kill()
	waitDead(t, pi)
	before := len(noticeTypes(t, spool))
	if err := pi.Start(); err != nil {
		t.Fatalf("respawn after Kill: %v", err)
	}
	if got := noticeTypes(t, spool); len(got) != before {
		t.Fatalf("respawn after Kill appended %v, want nothing", got[before:])
	}
}

// E4: on daemon SIGTERM pi dies first (SIGTERM→SIGKILL after the grace).
// Table: a pi that honors SIGTERM is not SIGKILLed; a pi that ignores it is.
func TestPiShutdownOrdering(t *testing.T) {
	cases := []struct {
		name   string
		script string
	}{
		{"honors sigterm", `trap 'exit 0' TERM; while read line; do :; done`},
		{"ignores sigterm until killed", `trap '' TERM; while read line; do :; done`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pi, spool, _ := newCrashPi(t, tc.script)
			pi.sleep = time.Sleep // Shutdown polls with real time
			if err := pi.Start(); err != nil {
				t.Fatalf("start: %v", err)
			}
			done := make(chan struct{})
			go func() {
				pi.Shutdown(100 * time.Millisecond)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Shutdown did not return")
			}
			waitDead(t, pi)
			for _, v := range noticeTypes(t, spool) {
				if v["type"] == "pi_restart" {
					t.Fatalf("intentional shutdown emitted pi_restart: %v", v)
				}
			}
		})
	}
}
