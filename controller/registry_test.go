package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/dzungtr/kape/controller/gen"
)

// Registry tests: the label-registry join logic (gateway ListSandboxes +
// in-memory live state) against a fake gateway — issue #175.

func newRegistryTest(t *testing.T) (*AgentManager, *FakeGateway, *FakeStream, *Agent) {
	t.Helper()
	gw := NewFakeGateway()
	stream := NewFakeStream()
	gw.SetStream(stream)
	mgr := NewAgentManager(gw, testConfig())
	agent, err := mgr.Create(context.Background(), &CreateProfile{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return mgr, gw, stream, agent
}

// TestCreateStampsOwnershipLabels pins AC 1: created sandboxes carry the
// managed-by and agent-id labels (visible via kubectl get sandbox
// --show-labels against the real gateway).
func TestCreateStampsOwnershipLabels(t *testing.T) {
	_, gw, _, agent := newRegistryTest(t)

	labels := gw.LabelsFor(agent.Sandbox)
	if labels[LabelManagedBy] != ManagedByValue {
		t.Fatalf("managed-by label = %q, want %q", labels[LabelManagedBy], ManagedByValue)
	}
	if labels[LabelAgentID] != agent.ID {
		t.Fatalf("agent-id label = %q, want %q", labels[LabelAgentID], agent.ID)
	}
}

// TestListJoinsLiveState pins the join: an in-memory agent's view comes from
// live state (source "live"), tracking the FSM through working → ready.
func TestListJoinsLiveState(t *testing.T) {
	mgr, _, stream, agent := newRegistryTest(t)

	views, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("List returned %d views, want 1", len(views))
	}
	v := views[0]
	if v.ID != agent.ID || v.Sandbox != agent.Sandbox || v.Status != StatusReady || v.Source != SourceLive {
		t.Fatalf("view = %+v, want live ready view for %s", v, agent.ID)
	}
	if v.Created == nil {
		t.Fatal("live view missing created timestamp")
	}

	// Live status tracks the FSM: prompt → working.
	if err := mgr.Prompt(agent.ID, "turn"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	views, _ = mgr.List(context.Background())
	if views[0].Status != StatusWorking || views[0].Source != SourceLive {
		t.Fatalf("in-turn view = %+v, want live working", views[0])
	}
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "settle")
	views, _ = mgr.List(context.Background())
	if views[0].Status != StatusReady {
		t.Fatalf("post-settle view status = %q, want ready", views[0].Status)
	}
}

// TestListExcludesForeignSandboxes pins AC 3: a sandbox created by hand (no
// managed-by label, or a foreign managed-by value) never appears, and an
// owned-label sandbox without an agent-id is excluded too.
func TestListExcludesForeignSandboxes(t *testing.T) {
	mgr, gw, _, agent := newRegistryTest(t)

	gw.SeedSandbox("sbx-handmade", "uuid-handmade", map[string]string{}, v1.SandboxPhase_SANDBOX_PHASE_READY)
	gw.SeedSandbox("sbx-other-ctl", "uuid-other", map[string]string{LabelManagedBy: "someone-else", LabelAgentID: "agent-x"}, v1.SandboxPhase_SANDBOX_PHASE_READY)
	gw.SeedSandbox("sbx-no-agent-id", "uuid-noaid", map[string]string{LabelManagedBy: ManagedByValue}, v1.SandboxPhase_SANDBOX_PHASE_READY)

	views, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("List returned %d views, want only the owned agent: %+v", len(views), views)
	}
	if views[0].ID != agent.ID {
		t.Fatalf("view id = %q, want %q", views[0].ID, agent.ID)
	}
}

// TestListSurvivesRestart pins AC 2: with empty in-memory state (fresh
// controller process), List still returns the agent, derived from gateway
// sandbox state with source "cr".
func TestListSurvivesRestart(t *testing.T) {
	_, gw, _, agent := newRegistryTest(t)

	// "Restart": a new AgentManager over the same gateway, no memory.
	restarted := NewAgentManager(gw, testConfig())
	views, err := restarted.List(context.Background())
	if err != nil {
		t.Fatalf("List after restart: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("post-restart List returned %d views, want 1", len(views))
	}
	v := views[0]
	if v.ID != agent.ID || v.Source != SourceCR {
		t.Fatalf("post-restart view = %+v, want cr-derived view for %s", v, agent.ID)
	}
	if v.Status != StatusReady {
		t.Fatalf("post-restart status = %q, want ready (sandbox READY)", v.Status)
	}
	if v.Created != nil {
		t.Fatal("cr-derived view must omit created (no DB)")
	}
}

// TestCRStatusFromPhase pins the phase → status mapping for CR-derived views.
func TestCRStatusFromPhase(t *testing.T) {
	_, gw, _, agent := newRegistryTest(t)
	restarted := NewAgentManager(gw, testConfig())

	for phase, want := range map[v1.SandboxPhase]AgentStatus{
		v1.SandboxPhase_SANDBOX_PHASE_READY:        StatusReady,
		v1.SandboxPhase_SANDBOX_PHASE_ERROR:        StatusFailed,
		v1.SandboxPhase_SANDBOX_PHASE_PROVISIONING: StatusCreating,
		v1.SandboxPhase_SANDBOX_PHASE_STOPPED:      StatusCreating,
	} {
		gw.SetPhase(agent.Sandbox, phase)
		view, err := restarted.GetView(context.Background(), agent.ID)
		if err != nil {
			t.Fatalf("GetView(phase %v): %v", phase, err)
		}
		if view.Status != want {
			t.Fatalf("phase %v → status %q, want %q", phase, view.Status, want)
		}
	}
}

// TestGetViewPostRestart pins AC 4: get_agent on a post-restart agent reports
// the CR-derived status without crashing (no exec state available).
func TestGetViewPostRestart(t *testing.T) {
	_, gw, _, agent := newRegistryTest(t)

	restarted := NewAgentManager(gw, testConfig())
	view, err := restarted.GetView(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("GetView after restart: %v", err)
	}
	if view.ID != agent.ID || view.Status != StatusReady || view.Source != SourceCR {
		t.Fatalf("view = %+v, want cr-derived ready view", view)
	}

	// Genuinely unknown agent id → ErrUnknownAgent, not a crash.
	if _, err := restarted.GetView(context.Background(), "agent-nope"); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("GetView(unknown) err = %v, want ErrUnknownAgent", err)
	}
}

// TestGetViewPrefersLiveState pins the join precedence: an in-memory agent's
// view always wins over the CR-derived view (live exec state is fresher).
func TestGetViewPrefersLiveState(t *testing.T) {
	mgr, _, stream, agent := newRegistryTest(t)

	// Gateway phase still READY, but the in-memory agent failed (pi exit).
	stream.Exit(1)
	waitFor(t, func() bool { return agent.Status() == StatusFailed }, "pi exit → failed")

	view, err := mgr.GetView(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("GetView: %v", err)
	}
	if view.Source != SourceLive || view.Status != StatusFailed {
		t.Fatalf("view = %+v, want live failed (live state wins over CR READY)", view)
	}
}

// TestHTTPListAndPostRestartGet exercises the REST surface: GET /agents
// returns the registry array, and GET /agents/{id} serves a cr-derived view
// with 200 after a simulated restart.
func TestHTTPListAndPostRestartGet(t *testing.T) {
	gw := NewFakeGateway()
	stream := NewFakeStream()
	gw.SetStream(stream)
	mgr := NewAgentManager(gw, testConfig())
	agent, err := mgr.Create(context.Background(), &CreateProfile{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h := NewRouter(NewAgentManager(gw, testConfig())) // fresh memory

	// GET /agents returns a bare JSON array of registry views.
	rec := rawJSON(t, h, "GET", "/agents")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /agents status = %d", rec.Code)
	}
	var views []AgentView
	if err := jsonUnmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatalf("bad array body %q: %v", rec.Body.String(), err)
	}
	if len(views) != 1 || views[0].ID != agent.ID || views[0].Source != SourceCR {
		t.Fatalf("views = %+v, want one cr-derived view for %s", views, agent.ID)
	}

	code, got := doJSON(t, h, "GET", "/agents/"+agent.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /agents/{id} status = %d, want 200", code)
	}
	if got["source"] != SourceCR || got["status"] != string(StatusReady) {
		t.Fatalf("post-restart get = %v, want cr-derived ready", got)
	}
}

// rawJSON issues a request and returns the recorder untouched (for array
// responses doJSON's map decode cannot handle).
func rawJSON(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestHTTPGetUnknownAgentIs404 keeps the pre-registry contract intact.
func TestHTTPGetUnknownAgentIs404(t *testing.T) {
	h, _, _, _ := newTestHandler(t)
	code, _ := doJSON(t, h, "GET", "/agents/agent-nope", nil)
	if code != http.StatusNotFound {
		t.Fatalf("get unknown agent status = %d, want 404", code)
	}
}
