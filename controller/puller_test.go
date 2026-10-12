package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- fake daemon: the D3 surface behind httptest ---

// fakeDaemon is an in-memory stand-in for the daemon stub (issue #210) used
// by the pull-transport tests: an appendable spool of {spool_id, data}
// envelopes, a long-poll /records, /status, and a recording /commands.
type fakeDaemon struct {
	mu       sync.Mutex
	token    string
	spool    []DaemonRecord
	nextID   uint64
	bootID   string
	piRun    bool
	commands []DaemonCommand
	// failNext makes the next N /records calls fail with 500 (backoff tests).
	failNext int
}

func newFakeDaemon() *fakeDaemon {
	return &fakeDaemon{nextID: 1, bootID: "boot-test", token: "s3cret"}
}

// Append adds a record to the spool, assigning the next spool_id (starts at
// 1, like the daemon Spool).
func (d *fakeDaemon) Append(raw string) DaemonRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec := DaemonRecord{SpoolID: d.nextID, Data: json.RawMessage(raw)}
	d.nextID++
	d.spool = append(d.spool, rec)
	return rec
}

func (d *fakeDaemon) FailNext(n int) { d.mu.Lock(); d.failNext = n; d.mu.Unlock() }

func (d *fakeDaemon) Commands() []DaemonCommand {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]DaemonCommand(nil), d.commands...)
}

func (d *fakeDaemon) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		writeJSON(w, http.StatusOK, DaemonStatus{AgentID: "agent-x", BootID: d.bootID, PiRunning: d.piRun})
	})
	mux.HandleFunc("GET /records", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.failNext > 0 {
			d.failNext--
			http.Error(w, "spool unavailable", http.StatusInternalServerError)
			return
		}
		var after uint64
		fmt.Sscanf(r.URL.Query().Get("after"), "%d", &after)
		var out []DaemonRecord
		for _, rec := range d.spool {
			if rec.SpoolID > after {
				out = append(out, rec)
			}
		}
		if out == nil {
			out = []DaemonRecord{}
		}
		writeJSON(w, http.StatusOK, map[string][]DaemonRecord{"records": out})
	})
	mux.HandleFunc("POST /commands", func(w http.ResponseWriter, r *http.Request) {
		var c DaemonCommand
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		d.mu.Lock()
		d.commands = append(d.commands, c)
		d.mu.Unlock()
		w.Header().Set("Location", "/commands/"+c.ID)
		writeJSON(w, http.StatusAccepted, c)
	})
	// Mirror the daemon stub's auth: bearer token, /healthz unauthenticated.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != d.token {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kape-daemon"`)
			http.Error(w, "missing or invalid Authorization credential", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// pullTestConfig returns a pull-transport config. Daemon addressing is not
// in config anymore (issue #214): tests inject clients via the manager's
// test seam (mgr.daemon, or mgr.resolver + mgr.daemonTransport).
func pullTestConfig(_ *httptest.Server) Config {
	cfg := testConfig() // exec baseline (image/provider overrides)
	cfg.Transport = TransportPull
	cfg.DaemonToken = "s3cret"
	return cfg
}

// handlerRT executes HTTP requests against an in-process handler — no
// sockets. The nono sandbox denies loopback TCP dial for the test process,
// so the HTTPDaemonClient's transport is pointed at the fake daemon this way
// (the wire path is exercised live by the #217 smoke lane instead).
type handlerRT struct{ h http.Handler }

func (t handlerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

// daemonClientFor returns a client whose transport runs against h directly.
func daemonClientFor(h http.Handler) *HTTPDaemonClient {
	c := NewHTTPDaemonClient("http://daemon.test", "s3cret")
	c.hc.Transport = handlerRT{h}
	return c
}

// pullTestAgent creates an agent under the pull transport against a fake
// daemon and drives it to StatusReady.
func pullTestAgent(t *testing.T) (*AgentManager, *FakeGateway, *fakeDaemon, *Agent) {
	t.Helper()
	d := newFakeDaemon()
	cfg := pullTestConfig(httptest.NewServer(nil)) // URL shape only; transport is in-process
	cfg.DaemonToken = "s3cret"
	gw := NewFakeGateway()
	mgr := NewAgentManager(gw, cfg, testStore(t))
	mgr.daemon = daemonClientFor(d.handler())
	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return mgr, gw, d, agent
}

// --- AC: front-door prompt → daemon /commands → records → hub/SSE client ---

func TestPullTransportPromptToRecordsEndToEnd(t *testing.T) {
	mgr, _, d, agent := pullTestAgent(t)

	if err := mgr.Prompt(agent.ID, "hello daemon"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if agent.Status() != StatusWorking {
		t.Fatalf("status after accepted prompt = %q, want working", agent.Status())
	}
	cmds := d.Commands()
	if len(cmds) != 1 || cmds[0].Type != "prompt" || cmds[0].ID == "" {
		t.Fatalf("daemon commands = %+v, want one prompt with command_id", cmds)
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(cmds[0].Payload, &payload); err != nil || payload.Message != "hello daemon" {
		t.Fatalf("prompt payload = %s (%v), want message", cmds[0].Payload, err)
	}

	// The turn's records flow through the spool verbatim; the puller drains
	// them into the hub and the settled record flips working → ready.
	d.Append(`{"type":"turn_started","turn_id":"t-1"}`)
	d.Append(`{"type":"text_delta","turn_id":"t-1","text":"hi"}`)
	d.Append(`{"type":"agent_settled","turn_id":"t-1"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "settle via pulled records")

	evs := agent.hub.Events()
	var pulled []Event
	for _, ev := range evs {
		if ev.Type == "agent_settled" {
			pulled = append(pulled, ev)
		}
	}
	if len(pulled) != 1 {
		t.Fatalf("agent_settled events = %d, want 1", len(pulled))
	}
	// Verbatim: the settled record is never re-wrapped (only the envelope's
	// seq is controller-added).
	if string(pulled[0].Data) != `{"type":"agent_settled","turn_id":"t-1"}` {
		t.Fatalf("pulled record re-wrapped: %s", pulled[0].Data)
	}
	// Client-facing envelope is {seq,type,data} — no spool_id leakage.
	var env map[string]any
	if err := json.Unmarshal(mustJSON(pulled[0]), &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if _, ok := env["spool_id"]; ok {
		t.Fatalf("client-facing envelope leaks spool_id: %s", pulled[0].Data)
	}
}

// --- AC: 409-equivalent while a turn is in flight; acceptance = 202 ---

func TestPullTransportPromptWhileTurnInFlightRejectedBeforePost(t *testing.T) {
	mgr, _, d, agent := pullTestAgent(t)

	if err := mgr.Prompt(agent.ID, "first"); err != nil {
		t.Fatalf("first Prompt: %v", err)
	}
	if err := mgr.Prompt(agent.ID, "second"); !errors.Is(err, ErrTurnInFlight) {
		t.Fatalf("second Prompt err = %v, want ErrTurnInFlight", err)
	}
	// The daemon never saw the rejected prompt (409-equivalent is
	// controller-side, D4).
	if got := len(d.Commands()); got != 1 {
		t.Fatalf("daemon saw %d commands, want 1 (rejected prompt never POSTed)", got)
	}
}

func TestPullTransportAbortRidesCommand(t *testing.T) {
	mgr, _, d, agent := pullTestAgent(t)
	// Abort with no turn in flight is rejected without a POST.
	if err := mgr.Abort(agent.ID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("abort on non-working err = %v, want ErrNotReady", err)
	}
	if err := mgr.Prompt(agent.ID, "work"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := mgr.Abort(agent.ID); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	cmds := d.Commands()
	if len(cmds) != 2 || cmds[1].Type != "abort" || cmds[1].ID == "" {
		t.Fatalf("daemon commands = %+v, want prompt+abort with command ids", cmds)
	}
}

// --- AC (#215): mid-turn steer reaches the daemon leg as type:steer; an
// idle steer is rejected controller-side and never POSTed ---

func TestPullTransportSteerRidesCommand(t *testing.T) {
	mgr, _, d, agent := pullTestAgent(t)

	// Idle steer: rejected before the POST, like the prompt 409 (D4).
	if err := mgr.Steer(agent.ID, "too early"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("idle Steer err = %v, want ErrNotReady", err)
	}
	if got := len(d.Commands()); got != 0 {
		t.Fatalf("daemon saw %d commands for idle steer, want 0", got)
	}

	if err := mgr.Prompt(agent.ID, "work"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := mgr.Steer(agent.ID, "use the test file"); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	cmds := d.Commands()
	if len(cmds) != 2 || cmds[1].Type != "steer" || cmds[1].ID == "" {
		t.Fatalf("daemon commands = %+v, want prompt+steer with command ids", cmds)
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(cmds[1].Payload, &payload); err != nil || payload.Message != "use the test file" {
		t.Fatalf("steer payload = %s (%v), want message", cmds[1].Payload, err)
	}
	// Steer is mid-turn: the FSM keeps running and status stays working.
	if agent.Status() != StatusWorking {
		t.Fatalf("status after steer = %q, want working", agent.Status())
	}

	// The fake pi leg consumes the steer record end-to-end: the daemon-side
	// fake pi accepts a steer line without error (daemon test path); here we
	// pin the settled turn still flows after the steer.
	d.Append(`{"type":"agent_settled","turn_id":"t-1"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "settle via pulled records after steer")
}

// TestSteerOnTerminatedAgentErrors mirrors the prompt policy for steer: a
// terminated agent errors as unknown (already removed) or not-ready.
func TestSteerOnUnknownAgentErrors(t *testing.T) {
	mgr, _, _, _ := pullTestAgent(t)
	if err := mgr.Steer("agent-nope", "x"); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("Steer unknown err = %v, want ErrUnknownAgent", err)
	}
}

// --- AC: SIGKILL mid-pull → restart → replay + re-pull from watermark → no
// gaps, no duplicates ---

func TestOriginWatermarkSurvivesRestartNoGapsNoDuplicates(t *testing.T) {
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
	if err := mgr.Prompt(agent.ID, "turn"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	// First two records of the turn land before the "SIGKILL".
	d.Append(`{"type":"turn_started","turn_id":"t-1"}`)
	d.Append(`{"type":"text_delta","turn_id":"t-1","text":"partial"}`)
	waitFor(t, func() bool { return agent.hub.Origin() == 2 }, "first two records pulled")
	if len(agent.hub.Events()) < 4 {
		t.Fatalf("hub events = %d, want at least 4 after two records pulled", len(agent.hub.Events()))
	}
	if wm := agent.hub.Origin(); wm != 2 {
		t.Fatalf("origin watermark = %d, want 2", wm)
	}

	// SIGKILL: drop the whole controller process state — close the store
	// (the process died; in-test this is the clean way to release the file
	// lock; the puller goroutine leaks harmlessly against the dead store) —
	// then restart.
	if err := store.Close(); err != nil {
		t.Fatalf("close store (SIGKILL): %v", err)
	}
	reopened, err := NewBoltEventStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })
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
	// The recovered hub resumes from the store-side watermark.
	if wm := reborn.hub.Origin(); wm != 2 {
		t.Fatalf("recovered watermark = %d, want 2", wm)
	}
	// The rest of the turn lands while the controller was down; the daemon
	// spool still holds records 1-4 (unacked — the ack is the next pull).
	if err := mgr2.Prompt(agent.ID, "should reject mid-turn"); !errors.Is(err, ErrTurnInFlight) {
		t.Fatalf("prompt on replayed mid-turn agent err = %v, want ErrTurnInFlight", err)
	}
	d.Append(`{"type":"agent_settled","turn_id":"t-1"}`)
	waitFor(t, func() bool { return reborn.Status() == StatusReady }, "settle after restart")
	_ = agent

	// No gaps, no duplicates: the full client-facing log for the agent has
	// contiguous seqs 1..N with each spool record exactly once.
	evs := reborn.hub.Events()
	var turnStartedCount int
	for i, ev := range evs {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("seq gap at %d: %d (want %d)", i, ev.Seq, i+1)
		}
		if ev.Type == "turn_started" {
			turnStartedCount++
		}
	}
	// The pulled turn records appear exactly once: turn_started once, and
	// exactly one pulled agent_settled record (the second is the lifecycle
	// event "agent.settled" with a different type).
	if turnStartedCount != 1 {
		t.Fatalf("pulled turn_started count = %d, want 1 (no duplicates)", turnStartedCount)
	}
	var pulledSettled int
	for _, ev := range evs {
		if ev.Type == "agent_settled" && strings.Contains(string(ev.Data), `"t-1"`) {
			pulledSettled++
		}
	}
	if pulledSettled != 1 {
		t.Fatalf("pulled agent_settled count = %d, want 1 (no duplicates)", pulledSettled)
	}
}

func TestPublishPulledDedupeConsumesNoSeq(t *testing.T) {
	h := testHub(t)
	if seq, err := h.PublishPulled(json.RawMessage(`{"type":"a"}`), 1); err != nil || seq != 1 {
		t.Fatalf("first PublishPulled = (%d,%v), want seq 1", seq, err)
	}
	// Re-pull of the same record: deduped no-op.
	if seq, err := h.PublishPulled(json.RawMessage(`{"type":"a"}`), 1); err != nil || seq != 0 {
		t.Fatalf("deduped PublishPulled = (%d,%v), want (0,nil)", seq, err)
	}
	// Out-of-order stale id: deduped too.
	if seq, _ := h.PublishPulled(json.RawMessage(`{"type":"b"}`), 1); seq != 0 {
		t.Fatalf("stale spool_id consumed seq %d, want no-op", seq)
	}
	// A lifecycle event interleaves without touching the watermark; the next
	// pulled record still advances it.
	h.PublishLifecycle("agent.started", map[string]interface{}{"agent_id": "agent-test", "status": StatusReady})
	if seq, err := h.PublishPulled(json.RawMessage(`{"type":"b"}`), 2); err != nil || seq != 3 {
		t.Fatalf("PublishPulled after lifecycle = (%d,%v), want seq 3", seq, err)
	}
	if h.Origin() != 2 {
		t.Fatalf("origin = %d, want 2", h.Origin())
	}
}

func TestPublishPulledStoreFailureLeavesWatermarkUnmoved(t *testing.T) {
	store, err := NewBoltEventStore(t.TempDir() + "/events.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	h, err := NewEventHub(store, "agent-x")
	if err != nil {
		t.Fatalf("NewEventHub: %v", err)
	}
	// Break the store by closing it mid-flight.
	store.Close() //nolint:errcheck
	if _, err := h.PublishPulled(json.RawMessage(`{"type":"a"}`), 1); err == nil {
		t.Fatal("PublishPulled over broken store: err = nil, want error")
	}
	if h.Origin() != 0 {
		t.Fatalf("origin after failed append = %d, want 0 (watermark unmoved)", h.Origin())
	}
}

// --- puller failure handling: backoff then recovery ---

func TestPullerBacksOffThenRecovers(t *testing.T) {
	d := newFakeDaemon()
	cfg := pullTestConfig(httptest.NewServer(nil))
	gw := NewFakeGateway()
	mgr := NewAgentManager(gw, cfg, testStore(t))
	mgr.daemon = daemonClientFor(d.handler())
	agent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Two failing polls, then the daemon recovers with a record.
	d.FailNext(2)
	d.Append(`{"type":"agent_settled","turn_id":"t-0"}`)
	waitFor(t, func() bool {
		for _, ev := range agent.hub.Events() {
			if ev.Type == "agent_settled" {
				return true
			}
		}
		return false
	}, "record pulled after backoff recovery")
}

// --- daemon HTTP client surface: auth, 401, status, command 202 ---

func TestHTTPDaemonClientAuthAndErrors(t *testing.T) {
	d := newFakeDaemon()
	bad := NewHTTPDaemonClient("http://daemon.test", "wrong-token")
	bad.hc.Transport = handlerRT{d.handler()}
	if _, err := bad.Records(context.Background(), 0, time.Second); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Records with bad token err = %v, want 401 error", err)
	}
	if _, err := bad.Command(context.Background(), DaemonCommand{ID: "c1", Type: "prompt"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Command with bad token err = %v, want 401 error", err)
	}

	good := daemonClientFor(d.handler())
	st, err := good.Status(context.Background())
	if err != nil || st.BootID == "" {
		t.Fatalf("Status = %+v (%v), want boot_id", st, err)
	}
	recs, err := good.Records(context.Background(), 0, time.Second)
	if err != nil || recs == nil {
		t.Fatalf("Records = %v (%v), want empty list", recs, err)
	}
	// Context cancellation propagates.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := good.Records(ctx, 0, time.Second); err == nil {
		t.Fatal("Records with cancelled ctx err = nil, want error")
	}
}

// --- AC: pull transport has NO static daemon address — resolution is from
// the Sandbox CR (issue #214, D7); only resolution inputs are configured ---

func TestPullTransportConfigValidation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Transport = TransportPull
	cfg.DaemonPort = "9090"
	cfg.SandboxNamespace = "openshell"
	cfg.ControllerServiceHost = "kape-controller.openshell.svc.cluster.local"
	cfg.ControllerServicePort = 8081
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid pull config rejected: %v", err)
	}
	var fe *FieldError
	cfg.DaemonPort = "http://x" // not a port
	err := cfg.Validate()
	if !errors.As(err, &fe) || fe.Field != "daemon-port" {
		t.Fatalf("err = %v, want daemon-port field error", err)
	}
	cfg.DaemonPort = "0"
	if err := cfg.Validate(); !errors.As(err, &fe) || fe.Field != "daemon-port" {
		t.Fatalf("err = %v, want daemon-port field error", err)
	}
	cfg.DaemonPort = "9090"
	cfg.SandboxNamespace = ""
	if err := cfg.Validate(); !errors.As(err, &fe) || fe.Field != "sandbox-namespace" {
		t.Fatalf("err = %v, want sandbox-namespace field error", err)
	}
	cfg.SandboxNamespace = "openshell"
	cfg.ControllerServiceHost = ""
	if err := cfg.Validate(); !errors.As(err, &fe) || fe.Field != "controller-service-host" {
		t.Fatalf("err = %v, want controller-service-host field error", err)
	}
	cfg.ControllerServiceHost = "kape-controller.openshell.svc.cluster.local"
	cfg.ControllerServicePort = 70000
	if err := cfg.Validate(); !errors.As(err, &fe) || fe.Field != "controller-service-port" {
		t.Fatalf("err = %v, want controller-service-port field error", err)
	}
	cfg.ControllerServicePort = 8081
	cfg.Transport = "carrier-pigeon"
	err = cfg.Validate()
	if !errors.As(err, &fe) || fe.Field != "transport" {
		t.Fatalf("err = %v, want transport field error", err)
	}
	cfg.Transport = TransportExec
	if err := cfg.Validate(); err != nil {
		t.Fatalf("exec transport without daemon config rejected: %v", err)
	}
}

func TestConfigTransportEnvAndFlags(t *testing.T) {
	t.Setenv("KAPE_TRANSPORT", "exec")
	t.Setenv("KAPE_DAEMON_PORT", "9100")
	t.Setenv("KAPE_SANDBOX_NAMESPACE", "sbx-ns")
	cfg := ConfigFromEnv()
	if cfg.Transport != TransportExec || cfg.DaemonPort != "9100" || cfg.SandboxNamespace != "sbx-ns" {
		t.Fatalf("env transport config = %q/%q/%q, want exec/9100/sbx-ns", cfg.Transport, cfg.DaemonPort, cfg.SandboxNamespace)
	}
}
