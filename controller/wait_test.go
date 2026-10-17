package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- issue #233: wait_agent blocking consumption surface (ADR-0085) ---
// REST GET /agents/{id}/wait tests run against the real router (prior art:
// http_test.go, liveness_test.go). The MCP tool wait_agent shares mgr.Wait,
// so the wake semantics are exercised once; parseWaitTimeout covers the
// shared timeout validation both transports use.

// publishAssessed appends an agent.assessed event the way the monitor loop
// (monitor.go assess) does — same PublishLifecycle path.
func publishAssessed(a *Agent, verdict, confidence string) {
	a.hub.PublishLifecycle(EventTypeAssessed, map[string]interface{}{
		"agent_id":    a.ID,
		"verdict":     verdict,
		"summary":     "summary for " + verdict,
		"confidence":  confidence,
		"reason":      "",
		"assessed_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

// waitRequest issues one blocking GET /agents/{id}/wait on the real router.
func waitRequest(t *testing.T, mgr *AgentManager, id, timeout string) (*httptest.ResponseRecorder, WaitResult) {
	t.Helper()
	q := ""
	if timeout != "" {
		q = "?timeout=" + timeout
	}
	req := httptest.NewRequest("GET", "/agents/"+id+"/wait"+q, nil)
	rec := httptest.NewRecorder()
	NewRouter(mgr).ServeHTTP(rec, req)
	var res WaitResult
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("wait response not JSON: %v", err)
		}
	}
	return rec, res
}

// waitAsync runs one wait on a goroutine and returns a poll func for the
// result plus the caller's done channel.
func waitAsync(mgr *AgentManager, id, timeout string) func() (bool, WaitResult) {
	done := make(chan WaitResult, 1)
	go func() {
		req := httptest.NewRequest("GET", "/agents/"+id+"/wait?timeout="+timeout, nil)
		rec := httptest.NewRecorder()
		NewRouter(mgr).ServeHTTP(rec, req)
		var res WaitResult
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		done <- res
	}()
	return func() (bool, WaitResult) {
		select {
		case r := <-done:
			return true, r
		default:
			return false, WaitResult{}
		}
	}
}

func TestWaitImmediateReturnIdleWithTerminalAssessment(t *testing.T) {
	mgr, _, stream, agent := newTestAgent(t)

	mgr.Prompt(agent.ID, "turn one")
	publishAssessed(agent, "finished", "certain")
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "settle")

	rec, res := waitRequest(t, mgr, agent.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(res.Changed) != 1 || res.Changed[0] != "state" {
		t.Fatalf("changed = %v, want [state]", res.Changed)
	}
	if res.State != SessionIdle {
		t.Fatalf("state = %q, want idle", res.State)
	}
	if res.Assessment == nil || res.Assessment.Verdict != "finished" {
		t.Fatalf("assessment = %+v, want terminal finished verdict", res.Assessment)
	}
}

// TestWaitNoRawRecordsEverCrossSurface: even with noisy verbatim records in
// the log, the wait response carries only the projected snapshot.
func TestWaitNoRawRecordsEverCrossSurface(t *testing.T) {
	mgr, _, stream, agent := newTestAgent(t)
	mgr.Prompt(agent.ID, "turn one")
	stream.Deliver(`{"type":"text_delta","text":"secret transcript tokens"}`)
	publishAssessed(agent, "looping", "likely")
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "settle")

	_, res := waitRequest(t, mgr, agent.ID, "")
	body, _ := json.Marshal(res)
	if strings.Contains(string(body), "secret transcript tokens") || strings.Contains(string(body), "text_delta") {
		t.Fatalf("raw record leaked into wait response: %s", body)
	}
}

func TestWaitUnknownAgent404(t *testing.T) {
	mgr, _, _, _ := newTestAgent(t)
	rec, _ := waitRequest(t, mgr, "nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestWaitInvalidTimeout400(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	for _, bad := range []string{"abc", "0", "-5", "1.5"} {
		rec, _ := waitRequest(t, mgr, agent.ID, bad)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("timeout %q: status = %d, want 400", bad, rec.Code)
		}
	}
}

func TestParseWaitTimeoutDefaultAndCap(t *testing.T) {
	d, err := parseWaitTimeout("")
	if err != nil || d != waitDefaultTimeout {
		t.Fatalf("default = %v err %v, want %v nil", d, err, waitDefaultTimeout)
	}
	d, err = parseWaitTimeout("99999")
	if err != nil || d != waitMaxTimeout {
		t.Fatalf("capped = %v err %v, want %v nil", d, err, waitMaxTimeout)
	}
	d, err = parseWaitTimeout("30")
	if err != nil || d != 30*time.Second {
		t.Fatalf("30s = %v err %v", d, err)
	}
}

func TestWaitWakeOnStateChangePrompt(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	poll := waitAsync(mgr, agent.ID, "10")
	time.Sleep(50 * time.Millisecond) // let the subscriber register
	mgr.Prompt(agent.ID, "turn one")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done, res := poll(); done {
			if len(res.Changed) != 1 || res.Changed[0] != "state" {
				t.Fatalf("changed = %v, want [state]", res.Changed)
			}
			if res.State != SessionRunning {
				t.Fatalf("state = %q, want running", res.State)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("wait did not wake on prompt (running transition)")
}

func TestWaitWakeOnSettleIdle(t *testing.T) {
	mgr, _, stream, agent := newTestAgent(t)
	mgr.Prompt(agent.ID, "turn one")
	poll := waitAsync(mgr, agent.ID, "10")
	time.Sleep(50 * time.Millisecond)
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "settle")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done, res := poll(); done {
			if res.Changed[0] != "state" || res.State != SessionIdle {
				t.Fatalf("changed=%v state=%q, want [state] idle", res.Changed, res.State)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("wait did not wake on settle")
}

func TestWaitWakeOnVerdictTransition(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	mgr.Prompt(agent.ID, "turn one")
	poll := waitAsync(mgr, agent.ID, "10")
	time.Sleep(50 * time.Millisecond)
	publishAssessed(agent, "healthy", "likely")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done, res := poll(); done {
			if len(res.Changed) != 1 || res.Changed[0] != "assessment" {
				t.Fatalf("changed = %v, want [assessment]", res.Changed)
			}
			if res.Assessment == nil || res.Assessment.Verdict != "healthy" {
				t.Fatalf("assessment = %+v, want healthy", res.Assessment)
			}
			if res.State != SessionRunning {
				t.Fatalf("state = %q, want running (no state change fired)", res.State)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("wait did not wake on first verdict")
}

// AC: a repeated identical verdict does NOT wake; confidence-only changes do
// NOT wake (v1 semantic, Handoffs row 3).
func TestWaitRepeatedVerdictAndConfidenceDoNotWake(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	mgr.Prompt(agent.ID, "turn one")
	publishAssessed(agent, "healthy", "likely")

	poll := waitAsync(mgr, agent.ID, "1")
	time.Sleep(50 * time.Millisecond)
	publishAssessed(agent, "healthy", "likely")  // repeated verdict
	publishAssessed(agent, "healthy", "certain") // confidence-only change
	if done, _ := poll(); done {
		t.Fatal("wait woke on repeated verdict / confidence-only change")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if done, res := poll(); done {
			if len(res.Changed) != 1 || res.Changed[0] != "timeout" {
				t.Fatalf("changed = %v, want [timeout]", res.Changed)
			}
			if res.State != SessionRunning {
				t.Fatalf("state = %q, want running", res.State)
			}
			if res.Assessment == nil || res.Assessment.Verdict != "healthy" {
				t.Fatalf("assessment = %+v, want latest healthy", res.Assessment)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("wait never returned on timeout")
}

// AC: verdict TRANSITION wakes (healthy → looping), even mid-turn.
func TestWaitWakeOnVerdictTransitionHealthyToLooping(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	mgr.Prompt(agent.ID, "turn one")
	publishAssessed(agent, "healthy", "likely")

	poll := waitAsync(mgr, agent.ID, "10")
	time.Sleep(50 * time.Millisecond)
	publishAssessed(agent, "looping", "uncertain")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done, res := poll(); done {
			if res.Changed[0] != "assessment" || res.Assessment == nil || res.Assessment.Verdict != "looping" {
				t.Fatalf("changed=%v assessment=%+v, want [assessment] looping", res.Changed, res.Assessment)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("wait did not wake on healthy→looping")
}

// AC: liveness failure wakes.
func TestWaitWakeOnLivenessFailure(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	mgr.Prompt(agent.ID, "turn one")
	poll := waitAsync(mgr, agent.ID, "10")
	time.Sleep(50 * time.Millisecond)
	mgr.fail(agent, "daemon died (test)")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done, res := poll(); done {
			if res.Changed[0] != "state" {
				t.Fatalf("changed = %v, want [state]", res.Changed)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("wait did not wake on liveness failure")
}

// AC: an agent whose liveness failure already happened returns immediately.
func TestWaitImmediateReturnAlreadyFailed(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	mgr.fail(agent, "daemon died (test)")
	rec, res := waitRequest(t, mgr, agent.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if res.Changed[0] != "state" {
		t.Fatalf("changed = %v, want [state]", res.Changed)
	}
}

// AC: multiple events coalesce — return once, latest state + latest
// assessment, changed listing every trigger kind that fired.
func TestWaitCoalescesStateAndAssessment(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	mgr.Prompt(agent.ID, "turn one")
	publishAssessed(agent, "healthy", "likely")

	poll := waitAsync(mgr, agent.ID, "10")
	time.Sleep(50 * time.Millisecond)
	publishAssessed(agent, "looping", "uncertain") // verdict transition
	// Liveness failure published directly on the hub: same wake window as
	// the verdict (a puller-driven settle lands on its own poll schedule,
	// outside the coalesce window — best-effort coalescing by design).
	agent.hub.PublishLifecycle("agent.failed", map[string]interface{}{
		"agent_id": agent.ID, "status": StatusFailed, "reason": "coalesce test",
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done, res := poll(); done {
			if len(res.Changed) != 2 || res.Changed[0] != "state" || res.Changed[1] != "assessment" {
				t.Fatalf("changed = %v, want [state assessment]", res.Changed)
			}
			if res.State != SessionRunning {
				t.Fatalf("state = %q, want running (fsm projection unchanged by agent.failed)", res.State)
			}
			if res.Assessment == nil || res.Assessment.Verdict != "looping" {
				t.Fatalf("assessment = %+v, want latest looping", res.Assessment)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("coalesced wait never returned")
}

// AC: concurrent waits on the same agent each get the next wake.
func TestWaitConcurrentWaitsEachGetNextWake(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	mgr.Prompt(agent.ID, "turn one")
	poll1 := waitAsync(mgr, agent.ID, "10")
	poll2 := waitAsync(mgr, agent.ID, "10")
	time.Sleep(50 * time.Millisecond)
	publishAssessed(agent, "looping", "uncertain")

	wakes := 0
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d1, r1 := poll1()
		d2, r2 := poll2()
		if d1 {
			wakes++
			if r1.Changed[0] != "assessment" {
				t.Fatalf("wait1 changed = %v, want [assessment]", r1.Changed)
			}
		}
		if d2 {
			wakes++
			if r2.Changed[0] != "assessment" {
				t.Fatalf("wait2 changed = %v, want [assessment]", r2.Changed)
			}
		}
		if wakes == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("concurrent wakes = %d, want 2", wakes)
}

// AC: timeout expiry returns the snapshot with changed ["timeout"] and the
// latest assessment included; already-idle with no assessment → null.
func TestWaitTimeoutIncludesLatestAssessment(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	mgr.Prompt(agent.ID, "turn one")
	publishAssessed(agent, "healthy", "certain")

	poll := waitAsync(mgr, agent.ID, "1")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if done, res := poll(); done {
			if len(res.Changed) != 1 || res.Changed[0] != "timeout" {
				t.Fatalf("changed = %v, want [timeout]", res.Changed)
			}
			if res.Assessment == nil || res.Assessment.Verdict != "healthy" {
				t.Fatalf("assessment = %+v, want healthy", res.Assessment)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed-out wait never returned")
}

func TestWaitNullAssessmentWhenTerminalSLMFailed(t *testing.T) {
	mgr, _, stream, agent := newTestAgent(t)
	mgr.Prompt(agent.ID, "turn one")
	// No agent.assessed ever lands (terminal SLM failed / budget exhausted).
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "settle")

	_, res := waitRequest(t, mgr, agent.ID, "")
	if res.State != SessionIdle {
		t.Fatalf("state = %q, want idle", res.State)
	}
	if res.Assessment != nil {
		t.Fatalf("assessment = %+v, want null", res.Assessment)
	}
}

// The MCP tool shares mgr.Wait; exercise the handler path directly.
func TestMCPWaitAgentHandler(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	h := handleWaitAgent(mgr)
	_, _, err := h(context.Background(), nil, waitInput{AgentID: "nope"})
	if err == nil {
		t.Fatal("unknown agent via MCP tool: want error")
	}
	_, _, err = h(context.Background(), nil, waitInput{AgentID: agent.ID, Timeout: "0"})
	if err == nil {
		t.Fatal("invalid timeout via MCP tool: want error")
	}
}

// consumeWakeEvents folds replayed events through the wake filter — the
// lost-wake path for events appended between the baseline snapshot and
// Subscribe (PR #241 review P1). Direct unit test: the gap is not
// deterministically reachable end-to-end.
func TestConsumeWakeEventsReplayPath(t *testing.T) {
	mk := func(typ string, verdict string) Event {
		data, _ := json.Marshal(map[string]string{"verdict": verdict, "confidence": "likely"})
		return Event{Seq: 1, Type: typ, Data: data}
	}
	changed := map[string]bool{}
	lv := "healthy"
	// Repeated verdict in the replay: suppressed, no wake.
	if consumeWakeEvents([]Event{mk(EventTypeAssessed, "healthy")}, changed, &lv) {
		t.Fatal("replayed repeated verdict woke")
	}
	// Verdict transition in the replay: wakes as assessment.
	if !consumeWakeEvents([]Event{mk(EventTypeAssessed, "looping")}, changed, &lv) {
		t.Fatal("replayed verdict transition did not wake")
	}
	if !changed["assessment"] || changed["state"] || lv != "looping" {
		t.Fatalf("changed=%v lastVerdict=%q, want assessment wake, lastVerdict looping", changed, lv)
	}
	// FSM lifecycle and liveness events in the replay: wake as state.
	changed = map[string]bool{}
	if !consumeWakeEvents([]Event{mk("agent.settled", ""), mk("agent.failed", "")}, changed, &lv) {
		t.Fatal("replayed state event did not wake")
	}
	if !changed["state"] || changed["assessment"] {
		t.Fatalf("changed=%v, want [state]", changed)
	}
	// Verbatim records never wake.
	changed = map[string]bool{}
	if consumeWakeEvents([]Event{mk("text_delta", ""), mk("raw", "")}, changed, &lv) {
		t.Fatal("replayed verbatim record woke")
	}
}
