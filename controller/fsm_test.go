package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
)

// TestReplaySessionDerivesStateFromLog pins the FSM projection: the session
// state is derived purely by replaying lifecycle events — never stored as
// separate truth (issue #196).
func TestReplaySessionDerivesStateFromLog(t *testing.T) {
	cases := []struct {
		name  string
		types []string
		want  SessionState
	}{
		{"empty log", nil, SessionProvision},
		{"provisioned", []string{"agent.provisioned"}, SessionProvision},
		{"started", []string{"agent.provisioned", "agent.started"}, SessionReady},
		{"turn started", []string{"agent.provisioned", "agent.started", "agent.turn_started"}, SessionRunning},
		{"settled", []string{"agent.provisioned", "agent.started", "agent.turn_started", "agent.settled"}, SessionIdle},
		{"second turn", []string{"agent.provisioned", "agent.started", "agent.turn_started", "agent.settled", "agent.turn_started"}, SessionRunning},
		{"terminated", []string{"agent.provisioned", "agent.started", "agent.turn_started", "agent.terminated"}, SessionTerminated},
		// Non-lifecycle records (verbatim pi records, stderr) do not move the state.
		{"pi records ignored", []string{"agent.started", "prompt", "text_delta", "agent_settled", "stderr"}, SessionReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var evs []Event
			for i, typ := range tc.types {
				evs = append(evs, Event{Seq: uint64(i + 1), Type: typ,
					Data: mustJSON(map[string]interface{}{"type": typ, "agent_id": "agent-x"})})
			}
			if got := ReplaySession(evs); got != tc.want {
				t.Fatalf("replay = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRestartMidLifecycleLandsOnIdenticalState is the restart determinism
// criterion: a SessionFSM rebuilt from the durable log after a controller
// restart lands on the identical state the live FSM held.
func TestRestartMidLifecycleLandsOnIdenticalState(t *testing.T) {
	store := testStore(t)
	hub, err := NewEventHub(store, "agent-rs")
	if err != nil {
		t.Fatalf("NewEventHub: %v", err)
	}
	fsm := NewSessionFSM(hub, "agent-rs", SessionProvision)
	if err := fsm.Transition(SessionReady, "agent.started", nil); err != nil {
		t.Fatalf("transition to ready: %v", err)
	}
	if err := fsm.Transition(SessionRunning, "agent.turn_started", nil); err != nil {
		t.Fatalf("transition to running: %v", err)
	}
	before := fsm.State()

	// Restart: fresh hub + FSM over the same store, state derived by replay.
	hub2, err := NewEventHub(store, "agent-rs")
	if err != nil {
		t.Fatalf("NewEventHub (restart): %v", err)
	}
	after, err := ReplaySessionFSM(hub2, "agent-rs")
	if err != nil {
		t.Fatalf("ReplaySessionFSM: %v", err)
	}
	if after.State() != before {
		t.Fatalf("restarted FSM state = %q, want %q", after.State(), before)
	}
	if before != SessionRunning {
		t.Fatalf("mid-lifecycle state = %q, want running", before)
	}
}

// appendFailingStore is an EventStore whose appends fail — a broken or
// full durable log. Reads still work.
type appendFailingStore struct{}

func (appendFailingStore) Agents() ([]string, error) { return nil, nil }

func (appendFailingStore) NextSeq(string) (uint64, error) { return 1, nil }
func (appendFailingStore) Append(string, Event) error     { return errors.New("append failed") }
func (appendFailingStore) Replay(string, uint64) ([]Event, error) {
	return nil, nil
}
func (appendFailingStore) Close() error { return nil }

// TestTransitionAppendsBeforeTakesEffect: a transition whose log append fails
// must not take effect — the in-memory state stays unchanged (appends-first
// invariant, issue #196).
func TestTransitionAppendsBeforeTakesEffect(t *testing.T) {
	hub, err := NewEventHub(appendFailingStore{}, "agent-x")
	if err != nil {
		t.Fatalf("NewEventHub: %v", err)
	}
	fsm := NewSessionFSM(hub, "agent-rs", SessionProvision)
	if err := fsm.Transition(SessionReady, "agent.started", nil); err == nil {
		t.Fatal("transition over failing store accepted, want error")
	}
	if fsm.State() != SessionProvision {
		t.Fatalf("state after failed append = %q, want unchanged provision", fsm.State())
	}
}

// TestTransitionRejectsInvalidTransitions pins the transition table and the
// reserved states: paused/reviving are in the vocabulary but unreachable —
// no legal edge reaches them until Epic 3 wires them (#192).
func TestTransitionRejectsInvalidTransitions(t *testing.T) {
	hub, err := NewEventHub(testStore(t), "agent-tt")
	if err != nil {
		t.Fatalf("NewEventHub: %v", err)
	}
	fsm := NewSessionFSM(hub, "agent-tt", SessionReady)
	for _, next := range []SessionState{SessionPaused, SessionReviving, SessionIdle, SessionProvision} {
		if err := fsm.Transition(next, "agent."+string(next), nil); err == nil {
			t.Fatalf("transition ready → %s accepted, want rejection", next)
		}
		if fsm.State() != SessionReady {
			t.Fatalf("state = %q after rejected transition, want ready", fsm.State())
		}
	}
	// Legal edge: ready → running, then running → terminated.
	if err := fsm.Transition(SessionRunning, "agent.turn_started", nil); err != nil {
		t.Fatalf("ready → running: %v", err)
	}
	if err := fsm.Transition(SessionTerminated, "agent.terminated", nil); err != nil {
		t.Fatalf("running → terminated: %v", err)
	}
	// Terminal: nothing leaves terminated.
	if err := fsm.Transition(SessionReady, "agent.started", nil); err == nil {
		t.Fatal("transition out of terminated accepted, want rejection")
	}
}

// TestSessionLifecycleEventsCarryStatus checks the lifecycle event payload
// contract: {type, agent_id, status, seq} in Data (spec #194 handoff #195).
func TestSessionLifecycleEventsCarryStatus(t *testing.T) {
	store := testStore(t)
	hub, err := NewEventHub(store, "agent-ev")
	if err != nil {
		t.Fatalf("NewEventHub: %v", err)
	}
	fsm := NewSessionFSM(hub, "agent-rs", SessionProvision)
	if err := fsm.Transition(SessionReady, "agent.started", nil); err != nil {
		t.Fatalf("transition: %v", err)
	}
	evs, err := store.Replay("agent-ev", 0)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("log has %d events, want 1", len(evs))
	}
	var data map[string]interface{}
	if err := json.Unmarshal(evs[0].Data, &data); err != nil {
		t.Fatalf("data not JSON: %v", err)
	}
	for _, k := range []string{"type", "agent_id", "status", "seq"} {
		if _, ok := data[k]; !ok {
			t.Fatalf("lifecycle data missing %q: %v", k, data)
		}
	}
	if data["status"] != string(SessionReady) {
		t.Fatalf("status = %v, want %q", data["status"], SessionReady)
	}
	if evs[0].Type != "agent.started" {
		t.Fatalf("envelope type = %q, want agent.started", evs[0].Type)
	}
}

// TestPrompt409DerivesFromFSMState: the 409 turn-in-flight boundary derives
// from the FSM session state — running rejects with ErrTurnInFlight, ready
// and idle accept.
func TestPrompt409DerivesFromFSMState(t *testing.T) {
	mgr, _, stream, agent := newTestAgent(t)

	if got := agent.fsm.State(); got != SessionReady {
		t.Fatalf("initial session state = %q, want ready", got)
	}
	if err := mgr.Prompt(agent.ID, "turn one"); err != nil {
		t.Fatalf("prompt in ready: %v", err)
	}
	if got := agent.fsm.State(); got != SessionRunning {
		t.Fatalf("session state after prompt = %q, want running", got)
	}
	if err := mgr.Prompt(agent.ID, "turn two"); !isErrTurnInFlight(err) {
		t.Fatalf("prompt while running: err = %v, want turn-in-flight", err)
	}
	// Turn settles → idle → prompt accepted again (202 path).
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return agent.fsm.State() == SessionIdle }, "settle → idle")
	if err := mgr.Prompt(agent.ID, "turn two"); err != nil {
		t.Fatalf("prompt while idle: %v", err)
	}
}

// TestAgentViewCarriesSessionStatus: the session lifecycle status lands on
// the agent view (session = durable unit, agent = live instance, #196).
func TestAgentViewCarriesSessionStatus(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	view := agent.agentView()
	if view.Session != SessionReady {
		t.Fatalf("view session = %q, want ready", view.Session)
	}
	if err := mgr.Prompt(agent.ID, "hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	view = agent.agentView()
	if view.Session != SessionRunning {
		t.Fatalf("view session after prompt = %q, want running", view.Session)
	}
	if view.Status != StatusWorking {
		t.Fatalf("agent status = %q, want working", view.Status)
	}
	if string(view.Session) == string(view.Status) && view.Session != "" {
		t.Fatalf("session %q collides with agent status vocabulary %q — session must be its own enum", view.Session, view.Status)
	}
}

// TestTransitionAtomicUnderRace pins the single-critical-section fix from
// the PR #205 round-3 review: two concurrent Transitions out of the same
// state (the settle-vs-Delete interleaving) must keep the in-memory state
// consistent with the durable log — exactly one append per successful
// transition, and the projection over the log must equal the live state.
// Under the old shape (validate and state-set in two separate f.mu
// sections, append outside the lock) both transitions validated against
// the stale running state, both appended, and the final state could land
// on either writer, diverging from the log projection.
func TestTransitionAtomicUnderRace(t *testing.T) {
	for i := 0; i < 100; i++ {
		store := testStore(t)
		hub, err := NewEventHub(store, "agent-race")
		if err != nil {
			t.Fatalf("NewEventHub: %v", err)
		}
		fsm := NewSessionFSM(hub, "agent-race", SessionProvision)
		// Seed the legal chain up to running so the log replays cleanly.
		if err := fsm.Transition(SessionReady, "agent.started", nil); err != nil {
			t.Fatalf("run %d: seed ready: %v", i, err)
		}
		if err := fsm.Transition(SessionRunning, "agent.turn_started", nil); err != nil {
			t.Fatalf("run %d: seed running: %v", i, err)
		}

		var wg sync.WaitGroup
		successes := make(chan SessionState, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := fsm.Transition(SessionIdle, "agent.settled", nil); err == nil {
				successes <- SessionIdle
			}
		}()
		go func() {
			defer wg.Done()
			if err := fsm.Transition(SessionTerminated, "agent.terminated", nil); err == nil {
				successes <- SessionTerminated
			}
		}()
		wg.Wait()
		close(successes)

		var winners []SessionState
		for s := range successes {
			winners = append(winners, s)
		}
		if len(winners) == 0 {
			t.Fatalf("run %d: no transition succeeded", i)
		}
		evs, err := store.Replay("agent-race", 0)
		if err != nil {
			t.Fatalf("run %d: replay: %v", i, err)
		}
		if len(evs) != 2+len(winners) {
			t.Fatalf("run %d: %d events appended, want %d", i, len(evs), 2+len(winners))
		}
		if got := ReplaySession(evs); got != fsm.State() {
			t.Fatalf("run %d: log projects %q, live state %q — atomicity broken", i, got, fsm.State())
		}
	}
}

// TestReservedStatesUnreachableUntilEpic3 guards the vocabulary: no code
// path in this epic emits the paused/reviving lifecycle events, so the FSM
// can never enter them.
func TestReservedStatesUnreachableUntilEpic3(t *testing.T) {
	for _, typ := range []string{"agent.paused", "agent.reviving"} {
		if sessionEventType[typ] != SessionPaused && sessionEventType[typ] != SessionReviving {
			t.Fatalf("event %q not mapped to a reserved state", typ)
		}
	}
	// And the transition table has legal edges only once resumed:
	if !sessionTransitions[SessionReviving][SessionReady] {
		t.Fatal("reviving → ready must be a legal edge for Epic 3 wiring")
	}
	if sessionTransitions[SessionProvision][SessionPaused] {
		t.Fatal("provision → paused must not be a legal edge")
	}
}

// TestReplaySessionFSError surfaces store read failures from replay-based
// FSM construction.
func TestReplaySessionFSError(t *testing.T) {
	hub, err := NewEventHub(failingStore{}, "agent-err")
	if err != nil {
		t.Fatalf("NewEventHub: %v", err)
	}
	if _, err := ReplaySessionFSM(hub, "agent-err"); err == nil {
		t.Fatal("ReplaySessionFSM on failing store: err = nil, want error")
	} else if !errors.Is(err, errSessionReplayFailed) && err.Error() == "" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// waitMS polls until the manager's live agent reaches session state want.
func waitMS(t *testing.T, mgr *AgentManager, id string, want SessionState) {
	t.Helper()
	waitFor(t, func() bool {
		a := mgr.Get(id)
		return a != nil && a.fsm.State() == want
	}, "session "+string(want))
}

// TestHTTPPromptBoundariesFromFSM pins the transport boundary: 409 while the
// session is running, 202 once it settles to idle — derived from the FSM
// state, not ad-hoc flags (issue #196). The agent view carries the session
// lifecycle status.
func TestHTTPPromptBoundariesFromFSM(t *testing.T) {
	h, mgr, _, stream := newTestHandler(t)

	_, create := doJSON(t, h, "POST", "/agents", nil)
	id := create["id"].(string)
	if create["session"] != string(SessionReady) {
		t.Fatalf("create response session = %v, want %q", create["session"], SessionReady)
	}

	code, _ := doJSON(t, h, "POST", "/agents/"+id+"/prompt", []byte(`{"message":"turn one"}`))
	if code != http.StatusAccepted {
		t.Fatalf("prompt while ready status = %d, want 202", code)
	}
	code, _ = doJSON(t, h, "POST", "/agents/"+id+"/prompt", []byte(`{"message":"turn two"}`))
	if code != http.StatusConflict {
		t.Fatalf("prompt while running status = %d, want 409", code)
	}
	_, view := doJSON(t, h, "GET", "/agents/"+id, nil)
	if view["session"] != string(SessionRunning) {
		t.Fatalf("view session = %v, want running", view["session"])
	}

	stream.Deliver(`{"type":"agent_settled"}`)
	waitMS(t, mgr, id, SessionIdle)
	code, _ = doJSON(t, h, "POST", "/agents/"+id+"/prompt", []byte(`{"message":"turn three"}`))
	if code != http.StatusAccepted {
		t.Fatalf("prompt while idle status = %d, want 202", code)
	}
	_, view = doJSON(t, h, "GET", "/agents/"+id, nil)
	if view["session"] != string(SessionRunning) {
		t.Fatalf("view session after second prompt = %v, want running", view["session"])
	}
}
