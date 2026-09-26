package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	v1 "github.com/dzungtr/kape/controller/gen"
)

// newTestAgent creates an AgentManager over a FakeGateway and drives the
// sandbox to READY + pi stream open, ending at StatusReady — the FSM's
// steady state for accepting prompts.
func newTestAgent(t *testing.T) (*AgentManager, *FakeGateway, *FakeStream, *Agent) {
	t.Helper()
	gw := NewFakeGateway()
	stream := NewFakeStream()
	gw.SetStream(stream)
	mgr := NewAgentManager(gw, testConfig())

	// Sandbox must be READY by the time the phase poll starts.
	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := agent.Status(); got != StatusReady {
		t.Fatalf("status after create = %q, want ready", got)
	}
	return mgr, gw, stream, agent
}

func TestCreateProvisionsAndReachesReady(t *testing.T) {
	mgr, gw, _, agent := newTestAgent(t)

	if len(gw.created) != 1 {
		t.Fatalf("created %v, want one sandbox", gw.created)
	}
	if got := mgr.Get(agent.ID); got == nil {
		t.Fatal("agent not registered")
	}
	if agent.Status() != StatusReady {
		t.Fatalf("status = %q, want ready", agent.Status())
	}
}

func TestPromptPolicySecondPromptDuringTurnIsRejected(t *testing.T) {
	mgr, _, stream, agent := newTestAgent(t)

	if err := mgr.Prompt(agent.ID, "turn one"); err != nil {
		t.Fatalf("first prompt: %v", err)
	}
	if got := agent.Status(); got != StatusWorking {
		t.Fatalf("status = %q, want working", got)
	}
	err := mgr.Prompt(agent.ID, "turn two")
	if err == nil {
		t.Fatal("second prompt accepted, want in-flight rejection")
	}
	if !isErrTurnInFlight(err) {
		t.Fatalf("err = %v, want turn-in-flight sentinel", err)
	}

	// Turn settles → back to ready → prompt accepted again.
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "settle → ready")
	if err := mgr.Prompt(agent.ID, "turn two"); err != nil {
		t.Fatalf("prompt after settle: %v", err)
	}
}

func TestPromptCommandShape(t *testing.T) {
	mgr, gw, _, agent := newTestAgent(t)
	if err := mgr.Prompt(agent.ID, "hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	sent := streamSent(t, gw, agent)
	if len(sent) != 1 {
		t.Fatalf("sent %d payloads, want 1", len(sent))
	}
	assertJSONFields(t, sent[0], map[string]string{"type": "prompt", "message": "hello"}, true /*id required*/)
}

func TestAbortStopsTurnButStreamStaysOpen(t *testing.T) {
	mgr, gw, stream, agent := newTestAgent(t)

	if err := mgr.Prompt(agent.ID, "runaway turn"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if err := mgr.Abort(agent.ID); err != nil {
		t.Fatalf("abort: %v", err)
	}
	// Abort command shape: {"type":"abort"} — verified against pi docs.
	sent := streamSent(t, gw, agent)
	if len(sent) != 2 {
		t.Fatalf("sent %d payloads, want 2 (prompt + abort)", len(sent))
	}
	assertJSONFields(t, sent[1], map[string]string{"type": "abort"}, false /*no id field*/)

	// Agent stays working until pi settles; abort does not break the stream.
	if got := agent.Status(); got != StatusWorking {
		t.Fatalf("status after abort = %q, want working (until settle)", got)
	}
	// Subsequent pi records still reach the event stream.
	stream.Deliver(`{"type":"message_update","content":"partial"}`)
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "settle after abort")

	evs := agent.hub.Events()
	last := string(evs[len(evs)-1])
	if last != `{"type":"agent_settled"}` {
		t.Fatalf("last event = %s, want verbatim agent_settled record", last)
	}
	// Abort outside a turn is rejected.
	if err := mgr.Abort(agent.ID); err == nil {
		t.Fatal("abort without in-flight turn accepted")
	}
}

func TestPiNonZeroExitMarksFailedAndEmitsLifecycle(t *testing.T) {
	mgr, _, stream, agent := newTestAgent(t)

	stream.Exit(1)
	waitFor(t, func() bool { return agent.Status() == StatusFailed }, "pi exit → failed")

	assertLifecycleEvent(t, agent, "agent.failed")
	_ = mgr
}

func TestExecStreamBreakMarksFailed(t *testing.T) {
	_, _, stream, agent := newTestAgent(t)

	stream.Fail(io.ErrUnexpectedEOF)
	waitFor(t, func() bool { return agent.Status() == StatusFailed }, "stream break → failed")
	assertLifecycleEvent(t, agent, "agent.failed")
}

func TestSandboxErrorPhaseMarksFailed(t *testing.T) {
	_, gw, _, agent := newTestAgent(t)

	gw.SetPhase(agent.Sandbox, v1.SandboxPhase_SANDBOX_PHASE_ERROR)
	waitFor(t, func() bool { return agent.Status() == StatusFailed }, "sandbox ERROR → failed")
	assertLifecycleEvent(t, agent, "agent.failed")
}

func TestDeleteMarksTerminatedAndDeletesSandbox(t *testing.T) {
	mgr, gw, _, agent := newTestAgent(t)

	if err := mgr.Delete(context.Background(), agent.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if agent.Status() != StatusTerminated {
		t.Fatalf("status = %q, want terminated", agent.Status())
	}
	if mgr.Get(agent.ID) != nil {
		t.Fatal("agent still registered after delete")
	}
	if got := gw.DeletedSandboxes(); len(got) != 1 || got[0] != agent.Sandbox {
		t.Fatalf("deleted sandboxes %v, want [%s]", got, agent.Sandbox)
	}
	// agent.terminated lifecycle event is on the (closed) event stream.
	evs := agent.hub.Events()
	last := string(evs[len(evs)-1])
	if !contains(last, `"type":"agent.terminated"`) {
		t.Fatalf("last event = %s, want agent.terminated lifecycle event", last)
	}
}

func TestPromptOnFailedAgentRejected(t *testing.T) {
	mgr, _, stream, agent := newTestAgent(t)

	stream.Exit(1)
	waitFor(t, func() bool { return agent.Status() == StatusFailed }, "fail")
	if err := mgr.Prompt(agent.ID, "again"); err == nil {
		t.Fatal("prompt on failed agent accepted")
	}
}

// --- helpers ---

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func jsonUnmarshal(b []byte, v interface{}) error { return json.Unmarshal(b, v) }

func isErrTurnInFlight(err error) bool {
	return errors.Is(err, ErrTurnInFlight)
}

// streamSent waits for stdinPump to drain the agent's stdin queue, then
// returns the payloads the FakeStream observed.
func streamSent(t *testing.T, gw *FakeGateway, a *Agent) [][]byte {
	t.Helper()
	// Wait until the stdin pump has recorded at least one payload on the
	// fake stream: len(stdin)==0 does not prove the pump has run yet.
	for i := 0; i < 50; i++ {
		if got := gw.stream.SentPayloads(); len(got) > 0 {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return gw.stream.SentPayloads()
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func assertLifecycleEvent(t *testing.T, a *Agent, eventType string) {
	t.Helper()
	for _, raw := range a.hub.Events() {
		if contains(string(raw), `"`+eventType+`"`) {
			return
		}
	}
	t.Fatalf("no %q lifecycle event in %d events", eventType, len(a.hub.Events()))
}

func assertJSONFields(t *testing.T, raw []byte, want map[string]string, requireID bool) {
	t.Helper()
	var m map[string]interface{}
	if err := jsonUnmarshal(raw, &m); err != nil {
		t.Fatalf("bad JSON payload %q: %v", raw, err)
	}
	for k, v := range want {
		if m[k] != v {
			t.Fatalf("field %q = %v, want %v", k, m[k], v)
		}
	}
	_, hasID := m["id"]
	if requireID && !hasID {
		t.Fatalf("payload %s missing id field", raw)
	}
	if !requireID && hasID {
		t.Fatalf("payload %s must not carry an id field (abort shape)", raw)
	}
}

// TestDeleteRacingAbortAndPromptDoesNotPanic exercises the P0 race from the
// PR #181 re-review: Delete closes agent.stdin; if Abort or sendPrompt can
// send after the close, the controller panics ("send on closed channel").
// The fix makes Delete flip the terminal status and close stdin in one
// agent.mu critical section, and makes Abort re-check status under the lock
// before sending. This test hammers the Delete-vs-Abort and Delete-vs-Prompt
// interleavings so the race detector (go test -race) sees any remaining
// unsynchronized send.
func TestDeleteRacingAbortAndPromptDoesNotPanic(t *testing.T) {
	for i := 0; i < 50; i++ {
		mgr, _, _, agent := newTestAgent(t)
		if err := mgr.Prompt(agent.ID, "turn"); err != nil {
			t.Fatalf("iteration %d: prompt: %v", i, err)
		}

		done := make(chan struct{}, 3)
		go func() {
			_ = mgr.Abort(agent.ID) // must return an error, never panic
			done <- struct{}{}
		}()
		go func() {
			_ = mgr.Prompt(agent.ID, "racing prompt") // rejected by FSM
			done <- struct{}{}
		}()
		go func() {
			if err := mgr.Delete(context.Background(), agent.ID); err != nil {
				t.Errorf("iteration %d: delete: %v", i, err)
			}
			done <- struct{}{}
		}()
		<-done
		<-done
		<-done

		if got := agent.Status(); got != StatusTerminated {
			t.Fatalf("iteration %d: status = %q, want terminated", i, got)
		}
		if mgr.Get(agent.ID) != nil {
			t.Fatalf("iteration %d: agent still registered", i)
		}
	}
}

// TestOperationsOnTerminatedAgentAreRejected pins the FSM contract the fix
// relies on: once Delete has run, prompt and abort on the agent object
// itself must return ErrNotReady — the status check that guards each channel
// send — rather than reaching the closed stdin channel.
func TestOperationsOnTerminatedAgentAreRejected(t *testing.T) {
	mgr, _, _, agent := newTestAgent(t)
	if err := mgr.Delete(context.Background(), agent.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Reach the agent object directly (manager lookups now 404) to prove
	// the send paths are guarded by status even past deregistration.
	if err := agent.sendPrompt("after delete"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("prompt on terminated agent: err = %v, want ErrNotReady", err)
	}

	// Simulate the racy Abort interleaving: the pre-lock status check has
	// already passed, then Delete completes, then Abort takes the lock and
	// must re-check before sending.
	agent.mu.Lock()
	agent.status = StatusWorking
	agent.mu.Unlock()
	if err := mgr.Abort(agent.ID); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("abort on deleted agent: err = %v, want ErrUnknownAgent", err)
	}
}
