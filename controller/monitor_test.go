package main

// Monitor loop tests (issue #232): fake SLM client + real BoltEventStore —
// arm/disarm on lifecycle transitions, tick pipeline (full-log window,
// agent.assessed append), silent-skip on SLM failure, bounded terminal
// assessment in the settle path, restart re-arm, and the minimal assessment
// projection. Prior art: registry_replay_test.go, liveness_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSLM records the pipeline calls and returns canned results.
type fakeSLM struct {
	mu             sync.Mutex
	summarizeN     int
	terminalN      int
	classifyN      int
	transcripts    []string
	telemetry      []TurnTelemetry
	err            error // fail the pipeline (silent-skip path)
	summary        string
	verdict        Verdict
	release        chan struct{} // closed to unblock
	terminalErr    error
	summarizeDelay time.Duration
}

func (f *fakeSLM) Summarize(ctx context.Context, transcript string) (string, error) {
	f.mu.Lock()
	f.summarizeN++
	f.transcripts = append(f.transcripts, transcript)
	delay, err := f.summarizeDelay, f.err
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	s := f.summary
	f.mu.Unlock()
	return s, nil
}

func (f *fakeSLM) SummarizeTerminal(ctx context.Context, transcript string) (string, error) {
	f.mu.Lock()
	f.terminalN++
	f.transcripts = append(f.transcripts, transcript)
	delay, err := f.summarizeDelay, f.terminalErr
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	s := f.summary
	f.mu.Unlock()
	return s, nil
}

func (f *fakeSLM) Classify(ctx context.Context, t TurnTelemetry) (Verdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.classifyN++
	f.telemetry = append(f.telemetry, t)
	if f.err != nil {
		return Verdict{}, f.err
	}
	return f.verdict, nil
}

func (f *fakeSLM) calls() (sum, term, class int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.summarizeN, f.terminalN, f.classifyN
}

// monitorHarness is a store + hub + monitor with a fast interval.
type monitorHarness struct {
	store *BoltEventStore
	hub   *EventHub
	slm   *fakeSLM
	mon   *Monitor
}

func newMonitorHarness(t *testing.T) *monitorHarness {
	t.Helper()
	store, err := NewBoltEventStore(t.TempDir() + "/events.db")
	if err != nil {
		t.Fatalf("event store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	hub, err := NewEventHub(store, "a1")
	if err != nil {
		t.Fatalf("hub: %v", err)
	}
	slm := &fakeSLM{summary: "working on it", verdict: Verdict{Verdict: VerdictHealthy, Confidence: "likely"}}
	mon := NewMonitor(slm, 5*time.Millisecond)
	return &monitorHarness{store: store, hub: hub, slm: slm, mon: mon}
}

// startTurn appends the turn_started lifecycle event with the prompt payload
// (what sendPrompt does on an accepted prompt).
func (h *monitorHarness) startTurn(t *testing.T, prompt string) {
	t.Helper()
	if h.hub.PublishLifecycle(lifecycleTurnStarted, map[string]interface{}{
		"agent_id": "a1", "status": SessionRunning, "prompt": prompt,
	}) == nil {
		t.Fatal("turn_started append failed")
	}
}

// waitAssessed polls the log until an agent.assessed event appears (or the
// deadline passes), returning it.
func (h *monitorHarness) waitAssessed(t *testing.T) *Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range h.hub.Events() {
			if ev.Type == EventTypeAssessed {
				return &ev
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

// TestMonitorTickAppendsAssessedWithFullLogWindow: each tick reads the full
// turn log (excluding agent.assessed) and appends agent.assessed.
func TestMonitorTickAppendsAssessedWithFullLogWindow(t *testing.T) {
	h := newMonitorHarness(t)
	h.startTurn(t, "fix the flaky test")
	h.hub.Publish([]byte(`{"type":"tool_call","name":"grep"}`))
	h.hub.Publish([]byte(`{"type":"tool_result","output":"3 hits"}`))

	h.mon.Arm("a1", h.hub)
	defer h.mon.Disarm("a1")

	ev := h.waitAssessed(t)
	if ev == nil {
		t.Fatal("no agent.assessed appended")
	}
	var fields struct {
		AssessmentPayload
		AgentID string `json:"agent_id"`
		Seq     uint64 `json:"seq"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(ev.Data, &fields); err != nil {
		t.Fatalf("assessed payload: %v", err)
	}
	if fields.Type != EventTypeAssessed || fields.AgentID != "a1" {
		t.Fatalf("envelope: type=%q agent=%q", fields.Type, fields.AgentID)
	}
	if fields.Verdict != VerdictHealthy || fields.Confidence != "likely" || fields.Summary != "working on it" {
		t.Fatalf("payload: %+v", fields.AssessmentPayload)
	}
	if fields.AssessedAt == "" {
		t.Fatal("assessed_at missing")
	}
	if _, err := time.Parse(time.RFC3339Nano, fields.AssessedAt); err != nil {
		t.Fatalf("assessed_at not RFC3339: %v", err)
	}

	// Full-log window (spec Q4): the transcript carries every event of the
	// turn since turn start (inclusive), minus agent.assessed.
	h.slm.mu.Lock()
	got := h.slm.transcripts[len(h.slm.transcripts)-1]
	h.slm.mu.Unlock()
	for _, want := range []string{"agent.turn_started", "fix the flaky test", "tool_call", "tool_result"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, EventTypeAssessed) {
		t.Fatalf("transcript must exclude agent.assessed:\n%s", got)
	}
}

// TestMonitorSilentSkipOnSLMFailure: SLM failure mid-turn → no event, counter
// incremented, turn unaffected (spec Q7).
func TestMonitorSilentSkipOnSLMFailure(t *testing.T) {
	h := newMonitorHarness(t)
	h.slm.mu.Lock()
	h.slm.err = errors.New("openrouter 429")
	before := skippedTicks.String()
	h.slm.mu.Unlock()
	h.startTurn(t, "prompt one")
	h.mon.Arm("a1", h.hub)
	defer h.mon.Disarm("a1")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.slm.mu.Lock()
		n := h.slm.summarizeN
		h.slm.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	for _, ev := range h.hub.Events() {
		if ev.Type == EventTypeAssessed {
			t.Fatalf("silent skip appended an event: %+v", ev)
		}
	}
	if skippedTicks.String() == before {
		t.Fatal("skip counter not incremented")
	}
}

// TestMonitorTerminalAssessmentBoundedAndBeforeSettled: the settle path runs
// the terminal assessment synchronously (SummarizeTerminal), bounded by the
// 10s budget; the assessment appends before agent.settled; failure → settle
// proceeds without a verdict.
func TestMonitorTerminalAssessmentBoundedAndBeforeSettled(t *testing.T) {
	h := newMonitorHarness(t)
	h.startTurn(t, "ship the PR")
	h.mon.Arm("a1", h.hub)
	<-time.After(15 * time.Millisecond) // let at least one mid-turn tick land

	// Settle path: disarm + terminal assessment, then agent.settled.
	h.mon.SettleAssess("a1")
	ev := h.waitAssessed(t)
	if ev == nil {
		t.Fatal("no terminal agent.assessed")
	}
	var p AssessmentPayload
	if err := json.Unmarshal(ev.Data, &p); err != nil {
		t.Fatal(err)
	}
	if p.Verdict != VerdictHealthy {
		t.Fatalf("terminal verdict: %+v", p)
	}
	sum, term, _ := h.slm.calls()
	if term != 1 {
		t.Fatalf("terminal summarizer calls = %d", term)
	}
	if sum+term < 2 {
		t.Fatalf("expected at least one mid-turn tick before settle (sum=%d)", sum)
	}

	// Disarmed: no further ticks after the settle.
	sum, term, class := h.slm.calls()
	time.Sleep(30 * time.Millisecond) // ~8 more tick periods — would double with 3 timers
	sum2, term2, class2 := h.slm.calls()
	if sum2 != sum || term2 != term || class2 != class {
		t.Fatalf("ticks after disarm: before (%d,%d,%d) after (%d,%d,%d)", sum, term, class, sum2, term2, class2)
	}
}

// TestMonitorTerminalAssessmentFailureSettleProceeds: SLM failure on the
// settle path appends nothing and returns within the budget.
func TestMonitorTerminalAssessmentFailureSettleProceeds(t *testing.T) {
	h := newMonitorHarness(t)
	h.startTurn(t, "ship the PR")
	h.slm.mu.Lock()
	h.slm.terminalErr = errors.New("openrouter down")
	h.slm.mu.Unlock()
	h.mon.Arm("a1", h.hub)

	start := time.Now()
	h.mon.SettleAssess("a1")
	if d := time.Since(start); d > terminalAssessBudget {
		t.Fatalf("settle blocked %s > budget", d)
	}
	for _, ev := range h.hub.Events() {
		if ev.Type == EventTypeAssessed {
			t.Fatal("failed terminal assessment appended an event")
		}
	}
	if skippedTicks.String() == "0" {
		t.Fatal("skip counter not incremented")
	}
}

// TestMonitorTerminalAssessmentBudgetBound: a hung SLM cannot hold settle
// indefinitely — the pipeline gives up at its context deadline instead of
// hanging. The budget itself is the fixed 10s terminalAssessBudget (not
// interval-scaled); here the 1s delayed fake is well inside it, so the test
// pins the non-hang property and the shape of the bound (the failure-path
// test above pins the prompt-return path).
func TestMonitorTerminalAssessmentBudgetBound(t *testing.T) {
	h := newMonitorHarness(t)
	h.startTurn(t, "ship the PR")
	h.slm.mu.Lock()
	h.slm.summarizeDelay = time.Second // far beyond a tick budget; inside the 10s terminal budget
	h.slm.mu.Unlock()
	mon := NewMonitor(h.slm, 50*time.Millisecond)
	mon.Arm("a1", h.hub)
	start := time.Now()
	mon.SettleAssess("a1")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("terminal assessment blocked %s", d)
	}
}

// TestMonitorOneTimerPerAgentNoAccumulation: re-arming a running agent never
// doubles the tick rate.
func TestMonitorOneTimerPerAgentNoAccumulation(t *testing.T) {
	h := newMonitorHarness(t)
	h.startTurn(t, "prompt")
	h.mon.Arm("a1", h.hub)
	h.mon.Arm("a1", h.hub)
	h.mon.Arm("a1", h.hub)
	defer h.mon.Disarm("a1")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.slm.mu.Lock()
		n := h.slm.summarizeN
		h.slm.mu.Unlock()
		if n >= 6 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.slm.mu.Lock()
	n := h.slm.summarizeN
	h.slm.mu.Unlock()
	time.Sleep(40 * time.Millisecond) // ~8 more tick periods — would double with 3 timers
	h.slm.mu.Lock()
	n2 := h.slm.summarizeN
	h.slm.mu.Unlock()
	if n2 > n+12 {
		t.Fatalf("tick rate suggests accumulated timers: %d → %d in 40ms", n, n2)
	}
}

// TestMonitorRestartReArm: a replayed running agent re-arms from replayed
// state; the new monitor's ticks assess the same turn window.
func TestMonitorRestartReArm(t *testing.T) {
	h := newMonitorHarness(t)
	h.startTurn(t, "long turn")
	h.hub.Publish([]byte(`{"type":"assistant","text":"still going"}`))
	h.mon.Arm("a1", h.hub)
	h.mon.SettleAssess("a1") // simulate: no terminal verdict, agent still mid-turn on replay

	// Controller restart: fresh monitor over a hub replayed from the store.
	hub2, err := NewEventHub(h.store, "a1")
	if err != nil {
		t.Fatal(err)
	}
	mon2 := NewMonitor(h.slm, 5*time.Millisecond)
	mon2.Arm("a1", hub2)
	defer mon2.Disarm("a1")

	if ev := h.waitAssessed(t); ev == nil {
		t.Fatal("re-armed monitor did not tick")
	}
}

// TestAssessmentProjection: the latest agent.assessed of the current turn
// projects as {verdict, summary, assessedAt, seq}; a new turn nulls it.
func TestAssessmentProjection(t *testing.T) {
	store, err := NewBoltEventStore(t.TempDir() + "/events.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hub, err := NewEventHub(store, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if hub.PublishLifecycle(lifecycleTurnStarted, map[string]interface{}{"agent_id": "a1"}) == nil {
		t.Fatal("append failed")
	}
	if hub.PublishLifecycle(EventTypeAssessed, map[string]interface{}{
		"agent_id": "a1", "verdict": "healthy", "summary": "s", "confidence": "likely",
		"reason": "", "assessed_at": "2026-01-01T00:00:00Z",
	}) == nil {
		t.Fatal("append failed")
	}
	evs := hub.Events()
	a := assessmentFromEvents(evs)
	if a == nil {
		t.Fatal("assessment not projected")
	}
	if a.Verdict != "healthy" || a.Summary != "s" || a.AssessedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("projection: %+v", a)
	}
	if a.Seq == 0 {
		t.Fatal("projection seq missing")
	}
	// A new turn nulls the previous turn's verdict (spec Q8).
	if hub.PublishLifecycle(lifecycleTurnStarted, map[string]interface{}{"agent_id": "a1"}) == nil {
		t.Fatal("append failed")
	}
	if a := assessmentFromEvents(hub.Events()); a != nil {
		t.Fatalf("stale assessment survived turn start: %+v", a)
	}
}

// TestAssessmentProjectionOnAgentView: the assessment property projects onto
// agentView (get_agent / list_agents items) from the durable log.
func TestAssessmentProjectionOnAgentView(t *testing.T) {
	store, err := NewBoltEventStore(t.TempDir() + "/events.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hub, err := NewEventHub(store, "a1")
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewAgentManager(nil, Config{}, store)
	a := &Agent{ID: "a1", hub: hub, fsm: NewSessionFSM(hub, "a1", SessionIdle), mgr: mgr}
	mgr.agents["a1"] = a
	// No assessment yet: null.
	if v := a.agentView(); v.Assessment != nil {
		t.Fatalf("expected nil assessment, got %+v", v.Assessment)
	}
	if hub.PublishLifecycle(lifecycleTurnStarted, map[string]interface{}{"agent_id": "a1"}) == nil {
		t.Fatal("append failed")
	}
	if hub.PublishLifecycle(EventTypeAssessed, map[string]interface{}{
		"agent_id": "a1", "verdict": "looping", "summary": "stuck",
		"confidence": "certain", "reason": "", "assessed_at": "2026-01-01T00:00:00Z",
	}) == nil {
		t.Fatal("append failed")
	}
	v := a.agentView()
	if v.Assessment == nil || v.Assessment.Verdict != "looping" || v.Assessment.Summary != "stuck" || v.Assessment.Seq == 0 {
		t.Fatalf("view assessment: %+v", v.Assessment)
	}
	// No confidence/reason in the projected shape (spec Q8): marshal the view
	// and assert the keys.
	raw, _ := json.Marshal(v.Assessment)
	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	for k := range keys {
		switch k {
		case "verdict", "summary", "assessedAt", "seq":
		default:
			t.Fatalf("unexpected projection key %q", k)
		}
	}
}

// TestTruncateRuneBoundary: truncation never splits a multi-byte rune
// (PR #238 review P2).
func TestTruncateRuneBoundary(t *testing.T) {
	s := "完了完了完了完了完了" // 10 runes × 3 bytes
	got := truncate(s, 7)
	for _, r := range got {
		if r == 0xFFFD {
			t.Fatalf("mid-rune split: %q", got)
		}
	}
	if got != s[:6] {
		t.Fatalf("truncate = %q, want %q", got, s[:6])
	}
	if truncate("abc", 10) != "abc" {
		t.Fatal("short string truncated")
	}
}

// TestSLMClientUsesConfiguredTimeoutNotClientTimeout: an injected zero-Timeout
// http.Client must not yield an instantly-expired context (PR #238 review P2)
// — the deadline comes from the stored MonitorConfig.Timeout.
func TestSLMClientUsesConfiguredTimeoutNotClientTimeout(t *testing.T) {
	// Injected transport: sleeps then returns a canned response (no network
	// needed — the sandbox blocks loopback dials).
	rt := delayedTransport{delay: 80 * time.Millisecond}
	cfg := MonitorConfig{BaseURL: "http://slm.test", Timeout: 5 * time.Second, SummarizerModel: "m"}
	// Injected client with zero Timeout: the old httpClient.Timeout-sourced
	// deadline expired instantly and failed every call.
	c := NewSLMClient(cfg, &http.Client{Timeout: 0, Transport: rt})
	out, err := c.Summarize(context.Background(), "transcript")
	if err != nil {
		t.Fatalf("summarize with zero-Timeout injected client: %v", err)
	}
	if out != "ok" {
		t.Fatalf("summarize = %q", out)
	}
	// And a short configured timeout still bounds the call: deadline wins.
	cfg.Timeout = 10 * time.Millisecond
	c2 := NewSLMClient(cfg, &http.Client{Timeout: 0, Transport: delayedTransport{delay: time.Second}})
	if _, err := c2.Summarize(context.Background(), "t"); err == nil {
		t.Fatal("configured timeout did not bound the call")
	}
}

// delayedTransport is a RoundTripper fake that sleeps then returns a
// chat-completions-shaped response.
type delayedTransport struct{ delay time.Duration }

func (d delayedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case <-time.After(d.delay):
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
		Header:     http.Header{},
	}, nil
}
