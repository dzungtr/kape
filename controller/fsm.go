package main

import (
	"fmt"
	"log"
	"sync"
)

// SessionState is the session lifecycle FSM state (issue #196): the durable
// session unit's position in provision → ready ⇄ running ⇄ idle →
// paused → running (Tier-2 revive, #251) → … → terminated. State is never
// stored as separate truth — it is a projection over the durable event log
// (EventStore, #195), derived by replaying lifecycle events.
type SessionState string

const (
	// SessionProvision: infrastructure provisioning (sandbox create submitted).
	SessionProvision SessionState = "provision"
	// SessionReady: infrastructure ready, pi stream open, no turn in flight.
	SessionReady SessionState = "ready"
	// SessionRunning: a prompt turn is in flight.
	SessionRunning SessionState = "running"
	// SessionIdle: the previous turn settled; the session may accept another
	// prompt (functionally equivalent to ready — the distinction is that
	// idle means at least one turn completed).
	SessionIdle SessionState = "idle"
	// SessionPaused: reserved for Epic 3 (#192) — checkpoint/pause; unreachable now.
	SessionPaused SessionState = "paused"
	// SessionReviving: reserved for Epic 3 (#192) — restore from checkpoint;
	// unreachable now.
	SessionReviving SessionState = "reviving"
	// SessionTerminated: terminal — the session was deleted.
	SessionTerminated SessionState = "terminated"
)

// sessionEventType maps a lifecycle event type to the session state it
// projects onto. These are the only event types the replay projection
// reacts to; every other record (verbatim pi records, stderr, exec_exit)
// leaves the state untouched.
var sessionEventType = map[string]SessionState{
	"agent.provisioned":  SessionProvision,
	"agent.started":      SessionReady,
	"agent.turn_started": SessionRunning,
	"agent.settled":      SessionIdle,
	lifecyclePaused:      SessionPaused,   // Tier-2 revive death detection (#251, spec #245 E5)
	lifecycleRevived:     SessionRunning,  // replacement sandbox daemon.boot + first pull (#251)
	"agent.reviving":     SessionReviving, // reserved: never emitted in M2
	"agent.terminated":   SessionTerminated,
}

// sessionTransitions is the FSM transition table: sessionTransitions[from][to]
// is true when the edge from → to is legal. Epic 3 (#192, #251) wires the
// Tier-2 revive edges: running → paused on sandbox death (spec #245 E5),
// paused → running on daemon.boot + the first successful pull from the
// replacement sandbox, paused → terminated as the Delete path. reviving stays
// reserved and unreachable (no event type maps onto it).

// lifecycle event names for the Tier-2 revive edges (#251). agent.paused and
// agent.revived are full session lifecycle events (ReplaySession projects
// them); the revive budget ledger rides non-lifecycle events (revive.go).
const (
	// lifecyclePaused: the sandbox/daemon died while a turn was in flight and
	// the session paused for Tier-2 revive (spec #245 E5).
	lifecyclePaused = "agent.paused"
	// lifecycleRevived: the replacement sandbox's daemon booted and the first
	// pull succeeded — the session resumed (paused → running).
	lifecycleRevived = "agent.revived"
)

var sessionTransitions = map[SessionState]map[SessionState]bool{
	SessionProvision: {SessionReady: true, SessionTerminated: true},
	SessionReady:     {SessionRunning: true, SessionTerminated: true},
	SessionRunning:   {SessionIdle: true, SessionPaused: true, SessionTerminated: true},
	SessionIdle:      {SessionRunning: true, SessionTerminated: true},
	// paused is reached by running → paused on sandbox death (#251): the
	// resume edge (paused → running) fires on daemon.boot + the first
	// successful pull from the replacement sandbox; Delete terminates.
	SessionPaused:     {SessionRunning: true, SessionTerminated: true},
	SessionReviving:   {SessionReady: true, SessionTerminated: true},
	SessionTerminated: {},
}

// errSessionReplayFailed wraps store read failures from replay-based FSM
// construction.
var errSessionReplayFailed = fmt.Errorf("session replay failed")

// ReplaySession folds the replay projection over a log slice: the session
// state after consuming every lifecycle event in order. Unknown or
// non-lifecycle types are ignored; the log is causally complete, so the
// result is deterministic.
func ReplaySession(evs []Event) SessionState {
	state := SessionProvision
	for i := range evs {
		if next, ok := sessionEventType[evs[i].Type]; ok && sessionTransitions[state][next] {
			state = next
		}
	}
	return state
}

// SessionFSM is the live projection: every transition appends its lifecycle
// event to the durable log BEFORE the state takes effect, so a restart
// derived by replay lands on the identical state. The in-memory state is a
// cache of the log, never an independent truth.
type SessionFSM struct {
	mu      sync.Mutex
	agentID string
	state   SessionState
	hub     *EventHub
}

// NewSessionFSM builds a live FSM whose starting state is known (fresh
// session, or restored by ReplaySessionFSM).
func NewSessionFSM(hub *EventHub, agentID string, initial SessionState) *SessionFSM {
	return &SessionFSM{agentID: agentID, state: initial, hub: hub}
}

// ReplaySessionFSM derives the FSM state for an existing session by
// replaying its durable log — the restart path (issue #196: restarting
// mid-lifecycle lands on the identical state).
func ReplaySessionFSM(hub *EventHub, agentID string) (*SessionFSM, error) {
	evs, err := hub.SubscribeSnapshot()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errSessionReplayFailed, err)
	}
	return NewSessionFSM(hub, agentID, ReplaySession(evs)), nil
}

// State returns the current session state.
func (f *SessionFSM) State() SessionState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

// Transition validates the edge, appends the lifecycle event to the durable
// log FIRST, and only then takes the transition effect (updates the state).
// The whole sequence runs under one critical section on f.mu: validate,
// append, and state-set are atomic with respect to any other Transition (or
// State read). PublishLifecycle takes only the hub's own lock, so holding
// f.mu across it creates no lock inversion. If the append fails the state is
// unchanged — durability wins over availability, matching the EventHub
// policy. Returns an error for an illegal edge or a failed append.
func (f *SessionFSM) Transition(next SessionState, eventType string, fields map[string]interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	from := f.state
	if !sessionTransitions[from][next] {
		return fmt.Errorf("illegal session transition %s → %s", from, next)
	}
	if fields == nil {
		fields = map[string]interface{}{}
	}
	fields["agent_id"] = f.agentID
	fields["status"] = next
	ev := f.hub.PublishLifecycle(eventType, fields)
	if ev == nil {
		// Append failed: no seq consumed, state unchanged.
		log.Printf("[fsm] append %s (%s → %s) failed; state unchanged", eventType, from, next)
		return fmt.Errorf("append %s failed", eventType)
	}
	f.state = next
	return nil
}
