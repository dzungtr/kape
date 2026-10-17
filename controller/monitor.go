package main

// SLM monitor loop (issue #232, spec #230 Q2/Q3/Q4/Q6/Q7/Q8/Q10): one timer
// per agent whose registry-projected state is running, armed on the
// agent.turn_started transition and disarmed on settle / termination /
// liveness failure. Timers are derived from registry lifecycle transitions —
// never persisted, never a separately-tracked counter; a controller restart
// re-arms from replayed state (RebuildFromLog), accepting ≤1 interval grace.
//
// On each tick the monitor reads the full event log since turn start
// (full-log window, spec Q4 — excluding agent.assessed so the monitor's own
// output never anchors its input), runs the SLM pipeline (summarize →
// classify) and appends an agent.assessed event to the same store via the
// agent's hub. SLM failure on a tick is a silent skip: log line + counter,
// no event; the next tick is the natural retry.
//
// Terminal assessment (spec Q10): the settle path disarms the timer and
// invalidates any in-flight tick BEFORE running the terminal assessment
// synchronously, hard-bounded by a 10s total budget; on failure or budget
// exhaustion settle proceeds immediately without a terminal verdict. The
// terminal assessment appends before the agent.settled event, so every
// settle wake carries the terminal verdict.

import (
	"context"
	"encoding/json"
	"expvar"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// EventTypeAssessed is the controller-owned assessment event appended by the
// monitor loop and the terminal settle path (spec #230 Q13). Envelope
// (Handoffs row 2, spec #230): {seq, type: "agent.assessed", data: {type,
// seq, agent_id, verdict, summary, confidence, reason, assessed_at}} —
// verdict is the closed six-value enum from slm.go (healthy | looping |
// blocked | degraded | finished | failed), confidence is uncertain | likely
// | certain, reason is "" on the decisions path (the Alpha decisions
// response carries no free-text reason), assessed_at is RFC3339Nano UTC.
const EventTypeAssessed = "agent.assessed"

// terminalAssessBudget bounds the whole terminal pipeline (summarize +
// classify + append) inside the settle path (spec #230 Q10): settle never
// blocks on the SLM beyond this budget.
const terminalAssessBudget = 10 * time.Second

// skippedTicks is the silent-skip counter (spec #230 Q7, Handoffs row 6):
// one increment per tick whose SLM pipeline failed (timeout, 429, 5xx,
// unparseable). expvar name: kape_monitor_skipped_ticks.
var skippedTicks = expvar.NewInt("kape_monitor_skipped_ticks")

// SLMPipeline is the assessment pipeline the monitor consumes — the SLM
// client surface from issue #231 (slm.go). An interface so tests inject a
// fake without an HTTP transport.
type SLMPipeline interface {
	Summarize(ctx context.Context, transcript string) (string, error)
	SummarizeTerminal(ctx context.Context, transcript string) (string, error)
	Classify(ctx context.Context, t TurnTelemetry) (Verdict, error)
}

// AssessmentPayload is the agent.assessed event payload (inside Data, alongside
// the envelope fields PublishLifecycle injects).
type AssessmentPayload struct {
	Verdict    string `json:"verdict"`
	Summary    string `json:"summary"`
	Confidence string `json:"confidence"`
	Reason     string `json:"reason"`
	AssessedAt string `json:"assessed_at"`
}

// AssessmentView is the minimal projected assessment property (spec #230
// Q8): exactly {verdict, summary, assessedAt, seq} — no confidence/reason;
// the full history stays in the event log. Null when the current turn has
// no assessment yet (a new turn's agent.turn_started nulls the previous
// turn's terminal verdict).
type AssessmentView struct {
	Verdict    string `json:"verdict"`
	Summary    string `json:"summary"`
	AssessedAt string `json:"assessedAt"`
	Seq        uint64 `json:"seq"`
}

// assessmentFromEvents projects the latest agent.assessed of the CURRENT turn
// onto the view shape: the last agent.assessed with seq > the last
// agent.turn_started seq. No turn started, or no assessment after it → nil.
func assessmentFromEvents(evs []Event) *AssessmentView {
	turnStart := uint64(0)
	latest := uint64(0)
	var payload AssessmentPayload
	for i := range evs {
		switch evs[i].Type {
		case lifecycleTurnStarted:
			turnStart = evs[i].Seq
		case EventTypeAssessed:
			var p AssessmentPayload
			if json.Unmarshal(evs[i].Data, &p) != nil {
				continue
			}
			if evs[i].Seq > latest {
				latest = evs[i].Seq
				payload = p
			}
		}
	}
	if latest == 0 || latest <= turnStart {
		return nil
	}
	return &AssessmentView{Verdict: payload.Verdict, Summary: payload.Summary, AssessedAt: payload.AssessedAt, Seq: latest}
}

// turnWindow is the full-log transcript window of the current turn (spec Q4):
// every event from the last agent.turn_started (inclusive) onward, minus the
// monitor's own agent.assessed output.
type turnWindow struct {
	turnStartSeq uint64
	prompt       string // the turn prompt recorded on agent.turn_started
	events       []Event
}

// currentTurnWindow scans the log for the last agent.turn_started and slices
// the window. found=false when no turn was ever started.
func currentTurnWindow(evs []Event) (turnWindow, bool) {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type != lifecycleTurnStarted {
			continue
		}
		var fields struct {
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(evs[i].Data, &fields)
		w := turnWindow{turnStartSeq: evs[i].Seq, prompt: fields.Prompt}
		for j := i; j < len(evs); j++ {
			if evs[j].Type != EventTypeAssessed {
				w.events = append(w.events, evs[j])
			}
		}
		return w, true
	}
	return turnWindow{}, false
}

// transcript renders the window for the summarizer: one line per event,
// "<type>: <verbatim record>".
func transcriptOf(w turnWindow) string {
	var b strings.Builder
	for i := range w.events {
		fmt.Fprintf(&b, "%s: %s\n", w.events[i].Type, string(w.events[i].Data))
	}
	return b.String()
}

// monitorEntry is the per-running-agent timer state: derived only, never
// persisted. gen is the invalidation guard — disarm and settle bump it, so a
// tick that already left the gate can never append after the disarm (no tick
// result ever appends after the terminal assessment, spec Q10). The entry
// survives its own disarm only for the settle path's terminal assessment.
type monitorEntry struct {
	mu   sync.Mutex
	gen  uint64
	stop chan struct{}

	hub       *EventHub
	armTime   time.Time
	lastRecAt time.Time
	lastRecTy string
}

// Monitor drives the per-agent assessment timers. Nil-safe methods: an
// unset monitor (disabled by config) makes every hook a no-op.
type Monitor struct {
	slm      SLMPipeline
	interval time.Duration
	now      func() time.Time // test seam

	mu     sync.Mutex
	agents map[string]*monitorEntry
}

// NewMonitor builds the monitor loop. interval is the per-running-agent tick
// period (MonitorConfig.Interval).
func NewMonitor(slm SLMPipeline, interval time.Duration) *Monitor {
	return &Monitor{slm: slm, interval: interval, now: time.Now, agents: map[string]*monitorEntry{}}
}

// Arm starts the timer for a running agent. Re-arming an already-armed agent
// is a no-op (one timer per running agent, no accumulation). The turn-start
// anchor and silence anchors are read from the durable log so the live
// transition path and the replay re-arm path converge on the same window.
func (m *Monitor) Arm(id string, hub *EventHub) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if _, ok := m.agents[id]; ok {
		m.mu.Unlock()
		return // already armed: never accumulate timers
	}
	e := &monitorEntry{hub: hub, stop: make(chan struct{})}
	m.agents[id] = e
	m.mu.Unlock()

	now := m.now()
	if evs, err := hub.SubscribeSnapshot(); err == nil {
		if w, found := currentTurnWindow(evs); found && len(w.events) > 0 {
			e.mu.Lock()
			e.lastRecTy = w.events[len(w.events)-1].Type
			e.mu.Unlock()
		}
	}
	e.mu.Lock()
	e.armTime = now
	e.lastRecAt = now
	e.mu.Unlock()
	go m.loop(id, e)
	log.Printf("[monitor] armed agent %s (interval %s)", id, m.interval)
}

// disarm stops the timer, invalidates any in-flight tick (the append gate
// closes), and returns the entry for the settle path's terminal assessment.
// Caller owns the returned entry; the timer goroutine is already gone.
func (m *Monitor) disarm(id string) *monitorEntry {
	m.mu.Lock()
	e, ok := m.agents[id]
	delete(m.agents, id)
	m.mu.Unlock()
	if !ok {
		return nil
	}
	e.mu.Lock()
	e.gen++ // in-flight tick: append gate now closed
	e.mu.Unlock()
	close(e.stop)
	return e
}

// Disarm stops the timer for a running agent (settle, termination, liveness
// failure). Unknown agent: no-op.
func (m *Monitor) Disarm(id string) {
	if m == nil {
		return
	}
	if e := m.disarm(id); e != nil {
		log.Printf("[monitor] disarmed agent %s", id)
	}
}

// SettleAssess is the settle path (spec Q10): disarm + invalidate in-flight
// ticks FIRST, then run the terminal assessment synchronously under the 10s
// total budget, appending agent.assessed BEFORE the caller appends
// agent.settled. Failure or budget exhaustion → settle proceeds without a
// terminal verdict (silent skip semantics, gap counted).
func (m *Monitor) SettleAssess(id string) {
	if m == nil {
		return
	}
	e := m.disarm(id)
	if e == nil || e.hub == nil {
		return
	}
	evs, err := e.hub.SubscribeSnapshot()
	if err != nil {
		log.Printf("[monitor] agent %s: terminal assessment read failed: %v", id, err)
		return
	}
	w, found := currentTurnWindow(evs)
	if !found {
		return
	}
	now := m.now()
	e.mu.Lock()
	armTime, lastRecAt, lastRecTy := e.armTime, e.lastRecAt, e.lastRecTy
	e.mu.Unlock()
	tel := TurnTelemetry{
		ElapsedSeconds:         now.Sub(armTime).Seconds(),
		SecondsSinceLastRecord: now.Sub(lastRecAt).Seconds(),
		LastRecordType:         lastRecTy,
		RecordCount:            len(w.events),
		TurnPrompt:             w.prompt,
	}
	m.assess(id, e.hub, e, w, tel, true)
}

// loop is the per-agent timer goroutine: one tick per interval until the
// stop channel closes. Never persisted; the goroutine dies with the disarm.
func (m *Monitor) loop(id string, e *monitorEntry) {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-t.C:
			m.tick(id, e)
		}
	}
}

// tick runs one assessment cycle: full-log window → summarize → classify →
// append agent.assessed. SLM failure = silent skip (log + counter, no
// event); a disarm after the tick left the gate closes the append.
func (m *Monitor) tick(id string, e *monitorEntry) {
	e.mu.Lock()
	hub, armTime, lastRecAt, lastRecTy := e.hub, e.armTime, e.lastRecAt, e.lastRecTy
	e.mu.Unlock()
	if hub == nil {
		return
	}
	evs, err := hub.SubscribeSnapshot()
	if err != nil {
		log.Printf("[monitor] agent %s: read log failed: %v", id, err)
		return
	}
	w, found := currentTurnWindow(evs)
	if !found {
		return // never turned: nothing to assess
	}
	tel := TurnTelemetry{
		ElapsedSeconds:         m.now().Sub(armTime).Seconds(),
		SecondsSinceLastRecord: m.now().Sub(lastRecAt).Seconds(),
		LastRecordType:         lastRecTy,
		RecordCount:            len(w.events),
		TurnPrompt:             w.prompt,
	}
	m.assess(id, hub, e, w, tel, false)
}

// assess runs summarize → classify → append. terminal selects the terminal
// summarizer prompt (deliverable references preserved verbatim). The append
// is gen-gated: a disarm or settle that bumped gen discards this tick's
// result (no tick result ever appends after the terminal assessment).
func (m *Monitor) assess(id string, hub *EventHub, e *monitorEntry, w turnWindow, tel TurnTelemetry, terminal bool) {
	budget := 2 * m.interval
	if terminal {
		budget = terminalAssessBudget
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	transcript := transcriptOf(w)
	var summary string
	var verdict Verdict
	var err error
	if terminal {
		summary, err = m.slm.SummarizeTerminal(ctx, transcript)
	} else {
		summary, err = m.slm.Summarize(ctx, transcript)
	}
	if err == nil {
		tel.Summary = summary
		verdict, err = m.slm.Classify(ctx, tel)
	}
	if err != nil {
		// Silent skip (spec Q7): log + counter, no event; next tick is the
		// natural retry.
		skippedTicks.Add(1)
		log.Printf("[monitor] agent %s: slm pipeline failed (skipped): %v", id, err)
		return
	}
	if !terminal {
		// Check-then-act must be atomic: gen read and the publish hold e.mu
		// together, so a disarm (gen bump) landing between them can never let
		// a stale tick append after the terminal assessment/agent.settled.
		e.mu.Lock()
		if e.gen != 0 { // bumped on disarm/settle → this tick is stale
			e.mu.Unlock()
			return
		}
		hub.PublishLifecycle(EventTypeAssessed, map[string]interface{}{
			"agent_id":    id,
			"verdict":     verdict.Verdict,
			"summary":     summary,
			"confidence":  verdict.Confidence,
			"reason":      verdict.Reason,
			"assessed_at": m.now().UTC().Format(time.RFC3339Nano),
		})
		e.mu.Unlock()
		return
	}
	hub.PublishLifecycle(EventTypeAssessed, map[string]interface{}{
		"agent_id":    id,
		"verdict":     verdict.Verdict,
		"summary":     summary,
		"confidence":  verdict.Confidence,
		"reason":      verdict.Reason,
		"assessed_at": m.now().UTC().Format(time.RFC3339Nano),
	})
}

// NoteRecord updates the silence anchors (last record time + type) from the
// record flow. Called from the record pumps; monitor-only bookkeeping.
func (m *Monitor) NoteRecord(id string, recType string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	e := m.agents[id]
	m.mu.Unlock()
	if e == nil {
		return
	}
	e.mu.Lock()
	e.lastRecAt = m.now()
	e.lastRecTy = recType
	e.mu.Unlock()
}
