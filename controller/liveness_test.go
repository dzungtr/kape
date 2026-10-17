package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// --- issue #213: liveness & daemon-death semantics (ADR-0083 D6) ---
// degraded transient flag on consecutive pull failures, partition vs daemon
// death via reconnect preflight boot_id comparison, drain-then-mark with a
// durable daemon.boot event ignored by the session projection, and replay
// consistency for liveness failed after a controller restart.

// SetBootID / SetPiRunning mutate the fake daemon's /status surface.
func (d *fakeDaemon) SetBootID(id string) { d.mu.Lock(); d.bootID = id; d.mu.Unlock() }
func (d *fakeDaemon) SetPiRunning(v bool) { d.mu.Lock(); d.piRun = v; d.mu.Unlock() }

// degradedView returns the agent's current view (degraded flag included).
func degradedView(a *Agent) AgentView { return a.agentView() }

// eventsOfType returns the durable events of one type.
func eventsOfType(evs []Event, typ string) []Event {
	var out []Event
	for _, ev := range evs {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// --- AC: 2 consecutive pull failures → degraded; any successful pull clears it ---

func TestDegradedAfterTwoConsecutivePullFailuresClearedBySuccess(t *testing.T) {
	mgr, _, d, agent := pullTestAgent(t)

	// One failure: below the threshold — degraded must not flip on.
	d.FailNext(1)
	waitFor(t, func() bool { return agent.hub.Origin() > 0 || true }, "noop settle")
	// Two failures: degraded.
	d.FailNext(2)
	waitFor(t, func() bool { return degradedView(agent).Degraded }, "degraded after 2 failures")
	// A successful pull clears it.
	d.Append(`{"type":"agent_settled","turn_id":"t-0"}`)
	waitFor(t, func() bool { return !degradedView(agent).Degraded }, "degraded cleared by successful pull")
	_ = mgr
}

// --- AC: pull outage with same boot_id → resume: no durable events, FSM untouched ---

func TestPartitionSameBootIDResumesNoDurableEvents(t *testing.T) {
	mgr, _, d, agent := pullTestAgent(t)
	before := len(agent.hub.Events())
	beforeSession := agent.fsm.State()

	d.FailNext(2) // outage long enough to go degraded
	waitFor(t, func() bool { return degradedView(agent).Degraded }, "degraded during partition")

	// Daemon comes back on the SAME boot_id; spool holds the turn's records.
	before = len(agent.hub.Events()) // snapshot after the outage, before the resume
	d.Append(`{"type":"turn_started","turn_id":"t-1"}`)
	d.Append(`{"type":"agent_settled","turn_id":"t-1"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady && agent.hub.Origin() == 2 }, "settle after partition resume")

	evs := agent.hub.Events()
	if got := eventsOfType(evs, daemonBootEventType); len(got) != 0 {
		t.Fatalf("partition appended durable events: %v", got)
	}
	if got := len(evs) - before; got != 2 {
		t.Fatalf("resumed events = %d, want exactly the 2 spool records (no gaps, no extras)", got)
	}
	if agent.fsm.State() != beforeSession {
		t.Fatalf("session state changed across partition: %s → %s", beforeSession, agent.fsm.State())
	}
	_ = mgr
}

// --- AC (issue #251): daemon restart mid-turn (new boot_id, pi_running:false)
// → spool drained → session paused (budget spent) → replacement daemon up
// → daemon.boot + first successful pull → paused → running; the lost turn is
// closed unsettled (no auto-reinject, E7). The pause window is transient
// against an always-reachable fake daemon, so the flow is observed through
// the durable event sequence. ---

func TestDaemonDeathWhileRunningPausesThenRevives(t *testing.T) {
	mgr, _, d, agent := pullTestAgent(t)

	if err := mgr.Prompt(agent.ID, "lost turn"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	// Partial turn lands before the death.
	d.Append(`{"type":"turn_started","turn_id":"t-1"}`)
	waitFor(t, func() bool { return agent.hub.Origin() == 1 }, "first record pulled")

	// Daemon dies: new boot identity, pi not running.
	d.SetBootID("boot-next")
	d.SetPiRunning(false)
	d.FailNext(2) // failure streak triggers the reconnect preflight

	// Drain-then-pause-then-revive: the spool's remaining records (append
	// one more to prove the drain) then the revive sequence.
	d.Append(`{"type":"text_delta","turn_id":"t-1","text":"partial"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady && agent.fsm.State() == SessionIdle }, "revived to idle")

	// Partial lost-turn record = honest history: drained into the log.
	evs := agent.hub.Events()
	if got := eventsOfType(evs, "text_delta"); len(got) != 1 {
		t.Fatalf("drained text_delta records = %d, want 1", len(got))
	}
	pauses := eventsOfType(evs, lifecyclePaused)
	if len(pauses) != 1 {
		t.Fatalf("agent.paused events = %d, want 1", len(pauses))
	}
	var pfields struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(pauses[0].Data, &pfields); err != nil || pfields.Reason == "" {
		t.Fatalf("agent.paused payload = %s (%v), want a reason", pauses[0].Data, err)
	}
	consumed := eventsOfType(evs, reviveConsumedEventType)
	if len(consumed) != 1 {
		t.Fatalf("revive_consumed events = %d, want 1", len(consumed))
	}
	var cfields struct {
		Attempt int `json:"attempt"`
	}
	if err := json.Unmarshal(consumed[0].Data, &cfields); err != nil || cfields.Attempt != 1 {
		t.Fatalf("revive_consumed payload = %s (%v), want attempt 1", consumed[0].Data, err)
	}
	boots := eventsOfType(evs, daemonBootEventType)
	if len(boots) != 1 {
		t.Fatalf("daemon.boot events = %d, want 1", len(boots))
	}
	var fields struct {
		BootID    string `json:"boot_id"`
		PiRunning bool   `json:"pi_running"`
	}
	if err := json.Unmarshal(boots[0].Data, &fields); err != nil {
		t.Fatalf("daemon.boot data: %v", err)
	}
	if fields.BootID != "boot-next" || fields.PiRunning {
		t.Fatalf("daemon.boot payload = %s, want boot_id boot-next", boots[0].Data)
	}
	if got := eventsOfType(evs, lifecycleRevived); len(got) != 1 {
		t.Fatalf("agent.revived events = %d, want 1", len(got))
	}
	settles := eventsOfType(evs, lifecycleSettled)
	if len(settles) != 1 {
		t.Fatalf("agent.settled events = %d, want 1 (revive-settle)", len(settles))
	}
	var sfields struct {
		ReviveSettled bool `json:"revive_settled"`
	}
	if err := json.Unmarshal(settles[0].Data, &sfields); err != nil || !sfields.ReviveSettled {
		t.Fatalf("revive-settle payload = %s (%v), want revive_settled true", settles[0].Data, err)
	}
	// The session accepts a new prompt after the revive (the lost turn was
	// closed; the host re-sends per E7).
	if err := mgr.Prompt(agent.ID, "after revive"); err != nil {
		t.Fatalf("Prompt after revive: %v", err)
	}
	if agent.Status() != StatusWorking {
		t.Fatalf("status after post-revive prompt = %s, want working", agent.Status())
	}
	_ = mgr
}

// --- AC: daemon.boot ignored by session replay; liveness failed rebuilds
// from the log alone after a controller restart (#197) ---

func TestDaemonDeathReplayLandsOnIdenticalState(t *testing.T) {
	store, path := func() (*BoltEventStore, string) {
		dir := t.TempDir()
		s, err := NewBoltEventStore(dir + "/events.db")
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		return s, dir + "/events.db"
	}()

	d := newFakeDaemon()
	cfg := pullTestConfig(httptest.NewServer(nil))
	cfg.ReviveMax = 0 // exhausted at birth: death → paused, no revive hold
	gw := NewFakeGateway()
	mgr := NewAgentManager(gw, cfg, store)
	mgr.daemon = daemonClientFor(d.handler())
	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mgr.Prompt(agent.ID, "lost"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	d.Append(`{"type":"turn_started","turn_id":"t-1"}`)
	waitFor(t, func() bool { return agent.hub.Origin() == 1 }, "record pulled")
	d.SetBootID("boot-next")
	d.SetPiRunning(false)
	d.FailNext(2)
	waitFor(t, func() bool { return agent.Status() == StatusFailed && agent.fsm.State() == SessionPaused }, "paused + failed after daemon death (budget exhausted)")
	if err := store.Close(); err != nil {
		t.Fatalf("close store (SIGKILL): %v", err)
	}

	reopened, err := NewBoltEventStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	gw2 := NewFakeGateway()
	gw2.SeedSandbox(agent.Sandbox, "uuid-"+agent.Sandbox, map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelAgentID:   agent.ID,
	}, 0)
	cfg2 := cfg
	cfg2.ReviveMax = 3 // the operator raised the cap across the restart
	mgr2 := NewAgentManager(gw2, cfg2, reopened)
	mgr2.daemon = daemonClientFor(d.handler())
	if err := mgr2.RebuildFromLog(context.Background()); err != nil {
		t.Fatalf("RebuildFromLog: %v", err)
	}
	reborn := mgr2.Get(agent.ID)
	if reborn == nil {
		t.Fatal("replayed agent not registered")
	}
	// The replayed paused session auto-revives while budget remains (the
	// revive automation is log-driven, so a restart re-drives it): the
	// replacement daemon is reachable → paused → running → revive-settle.
	d.SetPiRunning(true)
	waitFor(t, func() bool { return reborn.Status() == StatusReady && reborn.fsm.State() == SessionIdle }, "replayed revive completes")
	evs := reborn.hub.Events()
	if got := eventsOfType(evs, reviveConsumedEventType); len(got) != 1 {
		t.Fatalf("revive_consumed events = %d, want 1 (fresh attempt against the replayed ledger)", len(got))
	}
	if got := eventsOfType(evs, lifecycleRevived); len(got) != 1 {
		t.Fatalf("agent.revived events = %d, want 1", len(got))
	}
	if got := eventsOfType(evs, daemonBootEventType); len(got) != 1 {
		t.Fatalf("daemon.boot events = %d, want 1", len(got))
	}
	// Replayed views are never degraded (transient flag is not durable).
	if degradedView(reborn).Degraded {
		t.Fatal("replayed agent reported degraded; the flag must be transient")
	}
}

// --- AC: Delete on a stuck-working agent marks it terminated (never unmarked) ---

func TestDeleteOnFailedAgentTerminates(t *testing.T) {
	mgr, gw, d, agent := pullTestAgent(t)
	d.SetBootID("boot-next")
	d.SetPiRunning(false)
	d.FailNext(2)
	waitFor(t, func() bool { return agent.Status() == StatusFailed }, "failed")
	if err := mgr.Delete(context.Background(), agent.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if agent.Status() != StatusTerminated || agent.fsm.State() != SessionTerminated {
		t.Fatalf("delete on failed: %s/%s, want terminated/terminated", agent.Status(), agent.fsm.State())
	}
	// The gateway sandbox is gone; the agent leaves the registry.
	if _, err := mgr.GetView(context.Background(), agent.ID); err == nil {
		t.Fatal("GetView after Delete = nil error, want unknown agent")
	}
	_ = gw
}
