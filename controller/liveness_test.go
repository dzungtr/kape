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

// --- AC: daemon restart mid-turn (new boot_id, pi_running:false) → spool
// drained → daemon.boot appended → liveness failed, session FSM stays running ---

func TestDaemonDeathDrainThenBootEventThenFailed(t *testing.T) {
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

	// Drain-then-mark: the spool's remaining records (none beyond the
	// partial history above — append one more to prove the drain) then
	// daemon.boot, then failed.
	d.Append(`{"type":"text_delta","turn_id":"t-1","text":"partial"}`)
	waitFor(t, func() bool { return agent.Status() == StatusFailed }, "failed after daemon death")

	// Partial lost-turn record = honest history: drained into the log.
	evs := agent.hub.Events()
	if got := eventsOfType(evs, "text_delta"); len(got) != 1 {
		t.Fatalf("drained text_delta records = %d, want 1", len(got))
	}
	// The durable daemon.boot event carries the new boot identity.
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
		t.Fatalf("daemon.boot payload = %s, want boot_id boot-next, pi_running false", boots[0].Data)
	}
	// Liveness failed; the session FSM stays running (no failed state; the
	// turn never settled).
	if agent.fsm.State() != SessionRunning {
		t.Fatalf("session state after daemon death = %s, want running", agent.fsm.State())
	}
	// Recovery is Delete → terminated only: no new puller work after death.
	d.Append(`{"type":"agent_settled","turn_id":"t-1"}`)
	if err := mgr.Delete(context.Background(), agent.ID); err != nil {
		t.Fatalf("Delete on failed agent: %v", err)
	}
	if agent.Status() != StatusTerminated || agent.fsm.State() != SessionTerminated {
		t.Fatalf("after Delete: status %s session %s, want terminated/terminated", agent.Status(), agent.fsm.State())
	}
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
	waitFor(t, func() bool { return agent.Status() == StatusFailed }, "failed after daemon death")
	beforeSession := agent.fsm.State()
	beforeStatus := agent.Status()
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
	mgr2 := NewAgentManager(gw2, cfg, reopened)
	mgr2.daemon = daemonClientFor(d.handler())
	if err := mgr2.RebuildFromLog(context.Background()); err != nil {
		t.Fatalf("RebuildFromLog: %v", err)
	}
	reborn := mgr2.Get(agent.ID)
	if reborn == nil {
		t.Fatal("replayed agent not registered")
	}
	// Liveness failed rebuilds from the log alone (agent.failed).
	if reborn.Status() != beforeStatus || beforeStatus != StatusFailed {
		t.Fatalf("replayed status = %s, want failed", reborn.Status())
	}
	// Session replay ignores daemon.boot: state is identical post-restart.
	if reborn.fsm.State() != beforeSession || beforeSession != SessionRunning {
		t.Fatalf("replayed session = %s, want running (daemon.boot ignored)", reborn.fsm.State())
	}
	// Replayed views are never degraded (transient flag is not durable).
	if degradedView(reborn).Degraded {
		t.Fatal("replayed agent reported degraded; the flag must be transient")
	}
	// The rebuilt view still carries the failed liveness and the recorded
	// daemon.boot stays in the log.
	if got := eventsOfType(reborn.hub.Events(), daemonBootEventType); len(got) != 1 {
		t.Fatalf("replayed daemon.boot events = %d, want 1", len(got))
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
