package main

// wait_agent blocking consumption surface (issue #233, ADR-0085, spec #230):
// one held connection that returns exactly once with a minimal coalesced
// snapshot {agentId, changed, state, assessment} — never raw records. Wake
// triggers, server-side, verbatim: (1) FSM state change (any lifecycle event
// in the session projection, plus agent.failed — the liveness failure the
// session vocabulary deliberately does not model, issue #196), (2) verdict
// TRANSITION on agent.assessed — a repeated identical verdict does NOT wake
// and confidence-only changes do NOT wake (v1 semantic: confidence is not
// projected; recorded in spec #230 Handoffs row 3 so it is not "fixed"
// later), (3) liveness failure (agent.failed), (4) timeout expiry returns
// the snapshot with changed:["timeout"]. Multiple events during one wait
// coalesce: return once, latest state + latest assessment, changed listing
// every trigger kind that fired. Implementation subscribes to the existing
// in-process EventHub SSE fan-out with this trigger filter — the same
// pattern the controller→daemon pull contract uses, mirrored outward.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// waitDefaultTimeout / waitMaxTimeout: the long-poll knobs (ADR-0085).
// Default 300s; the server clamps any larger request to the 600s cap. A
// timeout that does not parse as a positive integer of seconds is a 400.
const (
	waitDefaultTimeout = 300 * time.Second
	waitMaxTimeout     = 600 * time.Second
)

// Wait trigger kinds — the closed vocabulary of the `changed` array.
const (
	waitChangedState      = "state"
	waitChangedAssessment = "assessment"
	waitChangedTimeout    = "timeout"
)

// waitCoalesceWindow is the grace window the first wake waits before
// returning, so near-simultaneous triggers (a verdict and the settle that
// follows it) coalesce into one response listing every kind that fired.
const waitCoalesceWindow = 100 * time.Millisecond

// WaitResult is the coalesced snapshot response (REST and MCP share the
// shape). Assessment is the latest projected AssessmentView of the current
// turn — null when none (including a terminal settle whose SLM failed).
type WaitResult struct {
	AgentID    string          `json:"agentId"`
	Changed    []string        `json:"changed"`
	State      SessionState    `json:"state"`
	Assessment *AssessmentView `json:"assessment"`
}

// parseWaitTimeout parses the `timeout` query/MCP param in seconds. Empty →
// the 300s default. Above the 600s server cap → clamped. Unparseable,
// zero, or negative → an error (HTTP 400 / MCP tool error).
func parseWaitTimeout(raw string) (time.Duration, error) {
	if raw == "" {
		return waitDefaultTimeout, nil
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return 0, fmt.Errorf("timeout must be a positive integer number of seconds")
	}
	d := time.Duration(secs) * time.Second
	if d > waitMaxTimeout {
		return waitMaxTimeout, nil
	}
	return d, nil
}

// wakeOf classifies one hub event against the trigger filter. Returns
// (kind, verdict) — verdict is non-empty only for assessment events, so the
// caller can track the last-seen verdict for transition suppression. Events
// that never wake (verbatim records, deltas, daemon.boot) return "".
func wakeOf(ev *Event) (string, string) {
	if _, ok := sessionEventType[ev.Type]; ok {
		// Any FSM lifecycle event is a state change (running→idle is the
		// main one) — including agent.terminated.
		return waitChangedState, ""
	}
	switch ev.Type {
	case "agent.failed":
		// Liveness failure (issue #213 D6): the session projection ignores
		// it, the wait surface must not.
		return waitChangedState, ""
	case EventTypeAssessed:
		var p AssessmentPayload
		if json.Unmarshal(ev.Data, &p) != nil {
			return "", ""
		}
		return waitChangedAssessment, p.Verdict
	}
	return "", ""
}

// Wait blocks until a wake trigger fires or the timeout expires, then
// returns the coalesced snapshot exactly once. Already-idle agents (and
// agents whose liveness failure already happened) return immediately with
// the current state and latest assessment; unknown/deleted agents return
// ErrUnknownAgent immediately. Concurrent waits on the same agent each get
// their own hub subscription and each observe the next wake.
func (m *AgentManager) Wait(ctx context.Context, id string, timeout time.Duration) (*WaitResult, error) {
	agent := m.Get(id)
	if agent == nil {
		return nil, errUnknownAgent(id)
	}
	hub, fsm := agent.hub, agent.fsm

	// Baseline snapshot: latest assessment + last-seen verdict of the
	// current turn, read once before any subscription.
	evs, err := hub.SubscribeSnapshot()
	if err != nil {
		return nil, err
	}
	base := assessmentFromEvents(evs)
	lastVerdict := ""
	if base != nil {
		lastVerdict = base.Verdict
	}
	state := fsm.State()

	// Already-settled (idle) or already-failed: the wake is in the past —
	// return immediately with the current snapshot.
	if state == SessionIdle || agent.Status() == StatusFailed {
		return &WaitResult{AgentID: id, Changed: []string{waitChangedState}, State: state, Assessment: base}, nil
	}

	// Subscribe from the baseline end: no gap between the snapshot and the
	// live channel (registration and replay share the hub lock).
	after := uint64(0)
	if len(evs) > 0 {
		after = evs[len(evs)-1].Seq
	}
	replay, live, unsub, err := hub.Subscribe(after)
	if err != nil {
		return nil, err
	}
	defer unsub()

	changed := map[string]bool{}
	// Lost-wake race (PR #241 review, P1): events appended between the
	// baseline snapshot and Subscribe — including settle/failed/assessed the
	// immediate-return checks just missed — arrive as Subscribe replay.
	// Consume the replay through the same wake filter BEFORE the select loop;
	// dropping it blocked a wake-worthy wait to timeout.
	if wake := consumeWakeEvents(replay, changed, &lastVerdict); wake {
		waitDrain(live, changed, &lastVerdict) // coalesce anything already queued live
		return m.waitSnapshot(id, "", changed)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			// Timeout expiry: current state + latest assessment, changed
			// ["timeout"].
			return m.waitSnapshot(id, waitChangedTimeout, changed)
		case ev, ok := <-live:
			if !ok {
				// Hub closed (agent deleted): the terminated lifecycle event
				// already fired as a state wake on the buffered channel —
				// this is the post-drain safe path.
				return m.waitSnapshot(id, waitChangedState, changed)
			}
			kind, verdict := wakeOf(ev)
			if kind == "" {
				continue
			}
			if kind == waitChangedAssessment && verdict == lastVerdict {
				// Repeated identical verdict: not a wake. Confidence-only
				// changes ride the same suppression — verdict unchanged.
				continue
			}
			if kind == waitChangedAssessment {
				lastVerdict = verdict
			}
			changed[kind] = true
			// Coalesce: hold the window open briefly and fold in every other
			// trigger that fires (verdict + settle landing together return
			// once, both kinds listed).
			waitDrain(live, changed, &lastVerdict)
			return m.waitSnapshot(id, "", changed)
		}
	}
}

// consumeWakeEvents folds a replayed event slice through the wake filter,
// accumulating trigger kinds and applying the lastVerdict suppression rule.
// Returns true when at least one wake fired. Shared by the Subscribe replay
// path and unit-tested directly (the snapshot→Subscribe gap is not
// deterministically reachable end-to-end).
func consumeWakeEvents(evs []Event, changed map[string]bool, lastVerdict *string) bool {
	woke := false
	for i := range evs {
		kind, verdict := wakeOf(&evs[i])
		if kind == "" {
			continue
		}
		if kind == waitChangedAssessment && verdict == *lastVerdict {
			continue
		}
		if kind == waitChangedAssessment {
			*lastVerdict = verdict
		}
		changed[kind] = true
		woke = true
	}
	return woke
}

// waitDrain consumes events for the coalesce window (bounded by
// waitCoalesceWindow), accumulating trigger kinds. Assessment verdicts
// update lastVerdict only on transitions.
func waitDrain(live chan *Event, changed map[string]bool, lastVerdict *string) {
	t := time.NewTimer(waitCoalesceWindow)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			return
		case ev, ok := <-live:
			if !ok {
				return
			}
			kind, verdict := wakeOf(ev)
			if kind == "" {
				continue
			}
			if kind == waitChangedAssessment && verdict == *lastVerdict {
				continue
			}
			if kind == waitChangedAssessment {
				*lastVerdict = verdict
			}
			changed[kind] = true
		}
	}
}

// waitSnapshot builds the response: latest projected state + assessment, no
// raw records. The changed list is deterministic order [state, assessment,
// timeout] filtered to what fired; kind merges one more trigger in. Store
// read failures are surfaced (hub contract: never a silent null assessment).
func (m *AgentManager) waitSnapshot(id, kind string, changed map[string]bool) (*WaitResult, error) {
	if kind != "" {
		changed[kind] = true
	}
	agent := m.Get(id)
	if agent == nil {
		// Deleted between wake and snapshot: report terminated, no assessment.
		return &WaitResult{AgentID: id, Changed: finalizeChanged(changed), State: SessionTerminated}, nil
	}
	state := agent.fsm.State()
	evs, err := agent.hub.SubscribeSnapshot()
	if err != nil {
		return nil, err
	}
	return &WaitResult{AgentID: id, Changed: finalizeChanged(changed), State: state, Assessment: assessmentFromEvents(evs)}, nil
}

// finalizeChanged serializes the trigger set in fixed order.
func finalizeChanged(changed map[string]bool) []string {
	out := make([]string, 0, 3)
	for _, k := range []string{waitChangedState, waitChangedAssessment, waitChangedTimeout} {
		if changed[k] {
			out = append(out, k)
		}
	}
	return out
}

// handleWait is the REST endpoint handler for GET /agents/{id}/wait.
func handleWait(mgr *AgentManager) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		timeout, err := parseWaitTimeout(r.URL.Query().Get("timeout"))
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		res, err := mgr.Wait(r.Context(), id, timeout)
		if err != nil {
			httpError(w, statusForErr(err), err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}
