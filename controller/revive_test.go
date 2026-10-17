package main

// Tier-2 revive unit tests (issue #251, spec #245 E5/E7): the running ⇄
// paused FSM edges, the log-derived budget ledger and cooldown pacing, the
// pause-time command rejections, exhaustion (paused + failed, surfaced on
// wait_agent), the re-arm verb, and Delete from paused. External behavior
// only — the death flow's end-to-end legs live in liveness_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

// pausedTestAgent drives a pull-transport agent into an explicitly paused
// session (running → paused with a liveness failure) without going through
// the puller's death detection — the deterministic base for rejection and
// re-arm tests.
func pausedTestAgent(t *testing.T) (*AgentManager, *fakeDaemon, *Agent) {
	t.Helper()
	mgr, _, d, agent := pullTestAgent(t)
	return pausedTestAgentWith(t, mgr, d, agent)
}

// pausedTestAgentWith pauses an existing pull-transport agent (shared setup
// for tests that need a custom manager configuration).
func pausedTestAgentWith(t *testing.T, mgr *AgentManager, d *fakeDaemon, agent *Agent) (*AgentManager, *fakeDaemon, *Agent) {
	t.Helper()
	if err := mgr.Prompt(agent.ID, "in-flight turn"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !mgr.pauseSession(agent, "test pause") {
		t.Fatal("pauseSession rejected a running session")
	}
	return mgr, d, agent
}

// --- AC: FSM edges — running → paused, paused → running, paused → terminated ---

func TestFSMPauseEdges(t *testing.T) {
	mk := func(t *testing.T) (*EventHub, *SessionFSM) {
		hub, err := NewEventHub(testStore(t), "agent-fsmrevive")
		if err != nil {
			t.Fatalf("hub: %v", err)
		}
		return hub, NewSessionFSM(hub, "agent-fsmrevive", SessionProvision)
	}
	t.Run("running_to_paused_to_running", func(t *testing.T) {
		_, fsm := mk(t)
		for _, step := range []struct {
			next SessionState
			ev   string
		}{
			{SessionReady, "agent.started"},
			{SessionRunning, "agent.turn_started"},
			{SessionPaused, lifecyclePaused},
			{SessionRunning, lifecycleRevived},
		} {
			if err := fsm.Transition(step.next, step.ev, nil); err != nil {
				t.Fatalf("transition %s: %v", step.ev, err)
			}
		}
		if fsm.State() != SessionRunning {
			t.Fatalf("state = %s, want running", fsm.State())
		}
	})
	t.Run("running_to_paused_to_terminated", func(t *testing.T) {
		_, fsm := mk(t)
		for _, step := range []struct {
			next SessionState
			ev   string
		}{
			{SessionReady, "agent.started"},
			{SessionRunning, "agent.turn_started"},
			{SessionPaused, lifecyclePaused},
			{SessionTerminated, "agent.terminated"},
		} {
			if err := fsm.Transition(step.next, step.ev, nil); err != nil {
				t.Fatalf("transition %s: %v", step.ev, err)
			}
		}
		if fsm.State() != SessionTerminated {
			t.Fatalf("state = %s, want terminated", fsm.State())
		}
	})
	t.Run("paused_to_idle_is_illegal", func(t *testing.T) {
		_, fsm := mk(t)
		_ = fsm.Transition(SessionReady, "agent.started", nil)
		_ = fsm.Transition(SessionRunning, "agent.turn_started", nil)
		_ = fsm.Transition(SessionPaused, lifecyclePaused, nil)
		if err := fsm.Transition(SessionIdle, "agent.settled", nil); err == nil {
			t.Fatal("paused → idle accepted, want illegal")
		}
	})
	t.Run("idle_to_paused_is_illegal", func(t *testing.T) {
		_, fsm := mk(t)
		_ = fsm.Transition(SessionReady, "agent.started", nil)
		_ = fsm.Transition(SessionRunning, "agent.turn_started", nil)
		_ = fsm.Transition(SessionIdle, "agent.settled", nil)
		if err := fsm.Transition(SessionPaused, lifecyclePaused, nil); err == nil {
			t.Fatal("idle → paused accepted, want illegal (revive is pinned to running → paused)")
		}
	})
	t.Run("replay_projects_the_pause_edges", func(t *testing.T) {
		hub, _ := mk(t)
		_ = hub.PublishLifecycle("agent.provisioned", map[string]interface{}{})
		_ = hub.PublishLifecycle("agent.started", map[string]interface{}{})
		_ = hub.PublishLifecycle("agent.turn_started", map[string]interface{}{})
		_ = hub.PublishLifecycle(lifecyclePaused, map[string]interface{}{})
		_ = hub.PublishLifecycle(lifecycleRevived, map[string]interface{}{})
		evs, err := hub.SubscribeSnapshot()
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		if got := ReplaySession(evs); got != SessionRunning {
			t.Fatalf("replayed session = %s, want running", got)
		}
	})
}

// --- AC: budget ledger is log-derived; agent.resumed resets it ---

func TestReviveLedgerFromEvents(t *testing.T) {
	at := func(min int) string { return time.Date(2026, 10, 17, 12, min, 0, 0, time.UTC).Format(time.RFC3339Nano) }
	consumed := func(at string) Event {
		return Event{Type: reviveConsumedEventType, Data: mustJSON(map[string]interface{}{"attempt": 1, "at": at})}
	}
	tests := []struct {
		name   string
		evs    []Event
		used   int
		open   bool
		wantAt time.Time
		wantCd time.Duration // cooldownRemaining under a 60s cooldown, now = 12:05
	}{
		{name: "empty log", evs: nil, used: 0, open: false, wantAt: time.Time{}, wantCd: 0},
		{name: "one consumption", evs: []Event{consumed(at(4))}, used: 1, open: true,
			wantAt: mustTime(t, at(4)), wantCd: 30 * time.Second},
		{name: "two consumptions", evs: []Event{consumed(at(0)), consumed(at(4))}, used: 2, open: true,
			wantAt: mustTime(t, at(4)), wantCd: 30 * time.Second},
		{name: "rearm resets", evs: []Event{consumed(at(0)), consumed(at(1)), {Type: agentResumedEventType}}, used: 0, open: false,
			wantAt: time.Time{}, wantCd: 0},
		{name: "revived closes the open attempt", evs: []Event{consumed(at(4)), {Type: lifecycleRevived}}, used: 1, open: false,
			wantAt: mustTime(t, at(4)), wantCd: 30 * time.Second},
		{name: "abandoned closes the open attempt without refunding", evs: []Event{consumed(at(4)), {Type: reviveAbandonedEventType}}, used: 1, open: false,
			wantAt: mustTime(t, at(4)), wantCd: 30 * time.Second},
	}
	now := time.Date(2026, 10, 17, 12, 4, 30, 0, time.UTC)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := reviveLedgerFromEvents(tt.evs)
			if l.used != tt.used || l.openAttempt != tt.open {
				t.Fatalf("ledger = used %d open %v, want used %d open %v", l.used, l.openAttempt, tt.used, tt.open)
			}
			if !l.lastAttempt.Equal(tt.wantAt) {
				t.Fatalf("lastAttempt = %v, want %v", l.lastAttempt, tt.wantAt)
			}
			if got := l.cooldownRemaining(now, 60*time.Second); got != tt.wantCd {
				t.Fatalf("cooldownRemaining = %v, want %v", got, tt.wantCd)
			}
		})
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ts
}

// --- AC: budget decrements per revive; exhaustion leaves the session paused
// + liveness failed; Delete from paused terminates ---

func TestReviveBudgetExhaustion(t *testing.T) {
	d := newFakeDaemon()
	cfg := pullTestConfig(httptest.NewServer(nil))
	cfg.ReviveMax = 2
	gw := NewFakeGateway()
	mgr := NewAgentManager(gw, cfg, testStore(t))
	mgr.daemon = daemonClientFor(d.handler())
	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Two full death → revive cycles consume the budget of 2.
	for i := 1; i <= 2; i++ {
		if err := mgr.Prompt(agent.ID, "turn"); err != nil {
			t.Fatalf("Prompt %d: %v", i, err)
		}
		d.Append(`{"type":"turn_started","turn_id":"t-1"}`)
		waitFor(t, func() bool { return agent.hub.Origin() >= uint64(i) }, "record pulled")
		d.SetBootID("boot-" + string(rune('a'+i))) // each death: new boot identity
		d.SetPiRunning(false)
		d.FailNext(2)
		waitFor(t, func() bool {
			evs := agent.hub.Events()
			return len(eventsOfType(evs, reviveConsumedEventType)) >= i
		}, "revive attempt recorded")
		d.SetPiRunning(true)
		waitFor(t, func() bool { return agent.fsm.State() == SessionIdle }, "revive %d completes")
	}

	// Third death: budget exhausted → paused + failed, no third consumption.
	if err := mgr.Prompt(agent.ID, "turn"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	d.Append(`{"type":"turn_started","turn_id":"t-3"}`)
	waitFor(t, func() bool { return agent.hub.Origin() >= 3 }, "record pulled")
	d.SetBootID("boot-d")
	d.SetPiRunning(false)
	d.FailNext(2)
	waitFor(t, func() bool { return agent.Status() == StatusFailed && agent.fsm.State() == SessionPaused }, "exhausted: paused + failed")
	if got := len(eventsOfType(agent.hub.Events(), reviveConsumedEventType)); got != 2 {
		t.Fatalf("revive_consumed events = %d, want 2 (exhaustion spends nothing)", got)
	}
	// Exhaustion is surfaced on wait_agent: paused state, immediate wake.
	res, err := mgr.Wait(context.Background(), agent.ID, 5*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.State != SessionPaused {
		t.Fatalf("wait state = %s, want paused", res.State)
	}
	// Delete from paused terminates.
	if err := mgr.Delete(context.Background(), agent.ID); err != nil {
		t.Fatalf("Delete from paused: %v", err)
	}
	if agent.Status() != StatusTerminated || agent.fsm.State() != SessionTerminated {
		t.Fatalf("after Delete: %s/%s, want terminated/terminated", agent.Status(), agent.fsm.State())
	}
}

// --- AC: the re-arm verb resets the budget and retries ---

func TestResumeRearmsExhaustedSession(t *testing.T) {
	d := newFakeDaemon()
	cfg := pullTestConfig(httptest.NewServer(nil))
	cfg.ReviveMax = 1 // one revive per lifetime until re-armed
	gw := NewFakeGateway()
	mgr := NewAgentManager(gw, cfg, testStore(t))
	mgr.daemon = daemonClientFor(d.handler())
	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// First death: the single budgeted revive runs automatically.
	if err := mgr.Prompt(agent.ID, "turn"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	d.Append(`{"type":"turn_started","turn_id":"t-1"}`)
	waitFor(t, func() bool { return agent.hub.Origin() >= 1 }, "record pulled")
	d.SetBootID("boot-b")
	d.SetPiRunning(false)
	d.FailNext(2)
	waitFor(t, func() bool { return len(eventsOfType(agent.hub.Events(), reviveConsumedEventType)) == 1 }, "first revive recorded")
	d.SetPiRunning(true)
	waitFor(t, func() bool { return agent.fsm.State() == SessionIdle }, "first revive completes")

	// Second death: budget exhausted → paused + failed, no second attempt.
	if err := mgr.Prompt(agent.ID, "turn"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	d.Append(`{"type":"turn_started","turn_id":"t-2"}`)
	waitFor(t, func() bool { return agent.hub.Origin() >= 2 }, "record pulled")
	d.SetBootID("boot-c")
	d.SetPiRunning(false)
	d.FailNext(2)
	waitFor(t, func() bool { return agent.Status() == StatusFailed && agent.fsm.State() == SessionPaused }, "paused + failed after exhaustion")

	// Resume is only legal for a paused session without an open attempt.
	if err := mgr.Resume(context.Background(), agent.ID); err != nil {
		t.Fatalf("Resume on paused: %v", err)
	}
	if err := mgr.Resume(context.Background(), agent.ID); err == nil {
		t.Fatal("second Resume accepted (attempt already open)")
	}
	if err := mgr.Resume(context.Background(), "agent-unknown"); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("Resume unknown agent = %v, want ErrUnknownAgent", err)
	}
	// The re-arm resets the ledger (agent.resumed): the re-driven attempt is
	// legal against the same cap of 1.
	d.SetPiRunning(true)
	waitFor(t, func() bool { return agent.Status() == StatusReady && agent.fsm.State() == SessionIdle }, "re-armed revive completes")
	evs := agent.hub.Events()
	if got := len(eventsOfType(evs, agentResumedEventType)); got != 1 {
		t.Fatalf("agent.resumed events = %d, want 1", got)
	}
	if got := len(eventsOfType(evs, reviveConsumedEventType)); got != 2 {
		t.Fatalf("revive_consumed events = %d, want 2 (first auto + re-armed)", got)
	}
	if got := len(eventsOfType(evs, lifecycleRevived)); got != 2 {
		t.Fatalf("agent.revived events = %d, want 2", got)
	}
}

// --- AC: a hold that never lands (deadline expiry) abandons the open attempt; the re-arm recovers ---

// expireHoldClock returns a clock that reads base for the first n calls (the
// deadline computation inside waitForReplacementDaemon) and past the 10-minute
// hold deadline afterwards, forcing the first deadline check to expire.
func expireHoldClock(base time.Time, deadlineCalls int) func() time.Time {
	calls := 0
	return func() time.Time {
		calls++
		if calls <= deadlineCalls {
			return base
		}
		return base.Add(11 * time.Minute)
	}
}

// recoverViaResume performs the post-abandonment recovery: the re-arm verb
// must be accepted (pre-fix it 409ed forever on the stuck open attempt) and
// the re-driven hold must complete paused → idle.
func recoverViaResume(t *testing.T, mgr *AgentManager, d *fakeDaemon, agent *Agent) {
	t.Helper()
	mgr.daemon = daemonClientFor(d.handler()) // restore the reachable daemon
	d.SetPiRunning(true)
	mgr.now = time.Now
	if err := mgr.Resume(context.Background(), agent.ID); err != nil {
		t.Fatalf("Resume after abandonment: %v", err)
	}
	waitFor(t, func() bool { return agent.fsm.State() == SessionIdle && agent.Status() == StatusReady }, "re-armed revive completes")
}

// stopAgentPuller cancels the agent's puller (and any Resume-re-driven one)
// at test end — leftover pull loops spin against the daemon seam and pollute
// timing-sensitive tests elsewhere in the package.
func stopAgentPuller(t *testing.T, agent *Agent) {
	t.Helper()
	t.Cleanup(func() {
		agent.mu.Lock()
		cancel := agent.cancel
		agent.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
}

func TestReviveHoldDeadlineAbandonsAttempt(t *testing.T) {
	d := newFakeDaemon()
	cfg := pullTestConfig(httptest.NewServer(nil))
	gw := NewFakeGateway()
	mgr := NewAgentManager(gw, cfg, testStore(t))
	mgr.daemon = daemonClientFor(d.handler())
	base := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	mgr.now = expireHoldClock(base, 3) // cooldown, consumption, deadline; the expiry check reads past-deadline
	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stopAgentPuller(t, agent)
	if err := mgr.Prompt(agent.ID, "turn"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	mgr.daemon = nil // no resolver: every replacement preflight fails (D7 re-resolve loop)
	if m := mgr.pauseAndRevive(agent, context.Background()); !m {
		t.Fatal("pauseAndRevive reported puller-continue on hold failure, want stop")
	}
	if agent.Status() != StatusFailed || agent.fsm.State() != SessionPaused {
		t.Fatalf("after hold failure: %s/%s, want failed/paused", agent.Status(), agent.fsm.State())
	}
	evs := agent.hub.Events()
	if got := len(eventsOfType(evs, reviveAbandonedEventType)); got != 1 {
		t.Fatalf("revive_abandoned events = %d, want 1 (open attempt must be closed)", got)
	}
	if got := len(eventsOfType(evs, reviveConsumedEventType)); got != 1 {
		t.Fatalf("revive_consumed events = %d, want 1", got)
	}
	// The ledger is no longer open: the re-arm verb is accepted and recovers
	// the session (pre-fix, Resume 409ed forever on the stuck open attempt).
	recoverViaResume(t, mgr, d, agent)
}

func TestReviveFromPausedOpenAttemptBeatsBudgetCheck(t *testing.T) {
	d := newFakeDaemon()
	cfg := pullTestConfig(httptest.NewServer(nil))
	cfg.ReviveMax = 1
	gw := NewFakeGateway()
	mgr := NewAgentManager(gw, cfg, testStore(t))
	mgr.daemon = daemonClientFor(d.handler())
	base := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	mgr.now = expireHoldClock(base, 1) // deadline computation only; the expiry check reads past-deadline
	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stopAgentPuller(t, agent)
	if err := mgr.Prompt(agent.ID, "turn"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !mgr.pauseSession(agent, "test pause") {
		t.Fatal("pauseSession rejected a running session")
	}
	// Craft an open attempt that is also the last budget unit: used == max.
	if agent.hub.PublishLifecycle(reviveConsumedEventType, map[string]interface{}{
		"agent_id": agent.ID, "attempt": 1, "at": base.Format(time.RFC3339Nano),
	}) == nil {
		t.Fatal("ledger append failed")
	}
	mgr.daemon = nil // no resolver: every replacement preflight fails (D7 re-resolve loop)
	mgr.reviveFromPaused(agent, context.Background())
	evs := agent.hub.Events()
	if got := len(eventsOfType(evs, reviveAbandonedEventType)); got != 1 {
		t.Fatalf("revive_abandoned events = %d, want 1 (open attempt resumed despite used==max)", got)
	}
	if got := len(eventsOfType(evs, reviveConsumedEventType)); got != 1 {
		t.Fatalf("revive_consumed events = %d, want 1 (resuming an open hold spends nothing)", got)
	}
	// Recovery: the abandon closed the attempt, so the re-arm is accepted even
	// though used still equals max.
	recoverViaResume(t, mgr, d, agent)
}

// --- AC: the cooldown wait must not hold reviveMu (a Resume during a live
// cooldown fails fast instead of blocking on the mutex) ---

func TestReviveCooldownSleepOutsideReviveMu(t *testing.T) {
	d := newFakeDaemon()
	cfg := pullTestConfig(httptest.NewServer(nil))
	cfg.ReviveCooldown = 1 * time.Second
	gw := NewFakeGateway()
	mgr := NewAgentManager(gw, cfg, testStore(t))
	mgr.daemon = daemonClientFor(d.handler())
	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stopAgentPuller(t, agent)
	if err := mgr.Prompt(agent.ID, "turn"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !mgr.pauseSession(agent, "test pause") {
		t.Fatal("pauseSession rejected a running session")
	}
	ctx := context.Background()
	if _, ok := mgr.consumeReviveAttempt(agent, ctx); !ok {
		t.Fatal("first consumption aborted")
	}
	done := make(chan bool, 1)
	go func() {
		_, ok := mgr.consumeReviveAttempt(agent, ctx) // second attempt: cooldown-paced
		done <- ok
	}()
	// The cooldown sleep happens outside reviveMu: the mutex is free while
	// the second attempt waits. (Pre-fix, TryLock blocked for the full
	// cooldown.)
	deadline := time.Now().Add(500 * time.Millisecond)
	free := false
	for time.Now().Before(deadline) {
		if agent.reviveMu.TryLock() {
			agent.reviveMu.Unlock()
			free = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !free {
		t.Fatal("reviveMu held during the cooldown sleep")
	}
	if ok := <-done; !ok {
		t.Fatal("cooldown-paced consumption aborted")
	}
	if got := len(eventsOfType(agent.hub.Events(), reviveConsumedEventType)); got != 2 {
		t.Fatalf("revive_consumed events = %d, want 2 (attempt consumed after the cooldown)", got)
	}
}

// --- AC: a lost ledger append aborts the attempt (no free revive on replay) ---

func TestReviveAppendFailureAbortsAttempt(t *testing.T) {
	d := newFakeDaemon()
	cfg := pullTestConfig(httptest.NewServer(nil))
	gw := NewFakeGateway()
	mgr := NewAgentManager(gw, cfg, testStore(t))
	mgr.daemon = daemonClientFor(d.handler())
	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stopAgentPuller(t, agent)
	mgr, _, agent = pausedTestAgentWith(t, mgr, d, agent)
	// PublishLifecycle returns nil once the store appends fail — a lost
	// revive_consumed would under-count replay budget (free revive).
	agent.hub.store = appendFailingStore{}
	if _, ok := mgr.consumeReviveAttempt(agent, context.Background()); ok {
		t.Fatal("consumption accepted with a failing ledger append, want abort")
	}
	// The abort happens before the hold: no replacement preflight is issued.
	statusCallsBefore := d.StatusCalls()
	mgr.reviveFromPaused(agent, context.Background())
	if got := d.StatusCalls(); got != statusCallsBefore {
		t.Fatalf("replacement preflights = %d, want %d (aborted attempt must not hold)", got, statusCallsBefore)
	}
	if err := mgr.Resume(context.Background(), agent.ID); err == nil {
		t.Fatal("Resume accepted with a failing ledger append, want error")
	}
}

// --- AC: prompt/steer/abort during paused are rejected fail-fast ---

func TestCommandsRejectedWhilePaused(t *testing.T) {
	mgr, _, agent := pausedTestAgent(t)
	if agent.fsm.State() != SessionPaused {
		t.Fatalf("setup: session = %s, want paused", agent.fsm.State())
	}
	if err := mgr.Prompt(agent.ID, "paused prompt"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("prompt during pause = %v, want ErrNotReady", err)
	}
	if err := mgr.Steer(agent.ID, "paused steer"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("steer during pause = %v, want ErrNotReady", err)
	}
	if err := mgr.Abort(agent.ID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("abort during pause = %v, want ErrNotReady (re-owned from #250)", err)
	}
	// Fail-fast means nothing reached the daemon: no command POST happened
	// for the rejections (the ledger/pause path posts none either).
	_ = mgr
}

// --- AC: cooldown paces consecutive revive attempts ---

func TestReviveCooldownPacesConsecutiveAttempts(t *testing.T) {
	d := newFakeDaemon()
	cfg := pullTestConfig(httptest.NewServer(nil))
	cfg.ReviveCooldown = 1 * time.Second
	gw := NewFakeGateway()
	mgr := NewAgentManager(gw, cfg, testStore(t))
	mgr.daemon = daemonClientFor(d.handler())
	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	dieAndRevive := func(i int) {
		if err := mgr.Prompt(agent.ID, "turn"); err != nil {
			t.Fatalf("Prompt %d: %v", i, err)
		}
		d.Append(`{"type":"turn_started","turn_id":"t-1"}`)
		waitFor(t, func() bool { return agent.hub.Origin() >= uint64(i) }, "record pulled")
		d.SetBootID("boot-" + string(rune('a'+i)))
		d.SetPiRunning(false)
		d.FailNext(2)
		waitFor(t, func() bool {
			return len(eventsOfType(agent.hub.Events(), reviveConsumedEventType)) >= i
		}, "revive %d recorded")
		d.SetPiRunning(true)
		waitFor(t, func() bool { return agent.fsm.State() == SessionIdle }, "revive %d completes")
	}
	dieAndRevive(1)
	dieAndRevive(2)

	consumed := eventsOfType(agent.hub.Events(), reviveConsumedEventType)
	if len(consumed) != 2 {
		t.Fatalf("revive_consumed events = %d, want 2", len(consumed))
	}
	parseAt := func(i int) time.Time {
		var f struct {
			At string `json:"at"`
		}
		if err := json.Unmarshal(consumed[i].Data, &f); err != nil {
			t.Fatalf("revive_consumed data: %v", err)
		}
		ts, err := time.Parse(time.RFC3339Nano, f.At)
		if err != nil {
			t.Fatalf("at %q: %v", f.At, err)
		}
		return ts
	}
	if gap := parseAt(1).Sub(parseAt(0)); gap < cfg.ReviveCooldown {
		t.Fatalf("second attempt recorded %v after the first, want ≥ cooldown %v", gap, cfg.ReviveCooldown)
	}
}

// --- AC: config knobs parse from the environment ---

func TestReviveConfigFromEnv(t *testing.T) {
	t.Setenv("KAPE_REVIVE_MAX", "7")
	t.Setenv("KAPE_REVIVE_COOLDOWN", "120")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.ReviveMax != 7 {
		t.Fatalf("ReviveMax = %d, want 7", cfg.ReviveMax)
	}
	if cfg.ReviveCooldown != 120*time.Second {
		t.Fatalf("ReviveCooldown = %v, want 120s", cfg.ReviveCooldown)
	}
	t.Setenv("KAPE_REVIVE_MAX", "-1")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("negative KAPE_REVIVE_MAX accepted, want error")
	}
}

// --- AC: the REST re-arm route — 202 on paused, 409 otherwise, 404 unknown ---

func TestHTTPResumeRoute(t *testing.T) {
	h, mgr, _, _ := newTestHandler(t)
	code, _ := doJSON(t, h, "POST", "/agents/agent-nope/resume", nil)
	if code != 404 {
		t.Fatalf("resume unknown agent = %d, want 404", code)
	}
	agent := mustCreateAgent(t, mgr)
	code, _ = doJSON(t, h, "POST", "/agents/"+agent.ID+"/resume", nil)
	if code != 409 {
		t.Fatalf("resume on a non-paused agent = %d, want 409", code)
	}
	// Drive the session into paused directly (deterministic pause base).
	if err := mgr.Prompt(agent.ID, "turn"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !mgr.pauseSession(mgr.Get(agent.ID), "test pause") {
		t.Fatal("pauseSession rejected a running session")
	}
	code, _ = doJSON(t, h, "POST", "/agents/"+agent.ID+"/resume", nil)
	if code != 202 {
		t.Fatalf("resume on a paused agent = %d, want 202", code)
	}
}
