package main

import (
	"context"
	"testing"
)

// Registry replay tests (issue #197): startup replays the durable event log
// into live registry state, so post-restart agent views are log-derived
// (source "replayed") instead of CR-derived, and read_events/stream_events
// cursors survive the restart. Sandbox CRs remain the address source.

// restartRegistry simulates a controller restart over the same gateway and
// the same durable store: fresh AgentManager memory, log replayed.
func restartRegistry(t *testing.T, gw *FakeGateway, store *BoltEventStore) *AgentManager {
	t.Helper()
	mgr := NewAgentManager(gw, testConfig(), store)
	if err := mgr.RebuildFromLog(context.Background()); err != nil {
		t.Fatalf("RebuildFromLog: %v", err)
	}
	return mgr
}

// viewCore strips the source field for the identical pre/post comparison:
// source records how the view was derived (live vs replayed) and is the one
// field that legitimately differs across a restart.
func viewCore(v AgentView) AgentView {
	v.Source = ""
	return v
}

// TestReplayIdenticalViews pins AC 1: after a restart, list_agents returns
// the same views (id, sandbox, status, session, created) as before — derived
// from log replay, not CR phases.
func TestReplayIdenticalViews(t *testing.T) {
	mgr, gw, stream, agent, store := newRegistryTestWithStore(t)

	// Drive a turn: working → settled, so the replayed status must come from
	// the log lifecycle events, not the (still READY) sandbox phase.
	if err := mgr.Prompt(agent.ID, "turn"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "settle")

	before, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("pre-restart List: %v", err)
	}
	if len(before) != 1 || before[0].Source != SourceLive {
		t.Fatalf("pre-restart views = %+v, want one live view", before)
	}

	restarted := restartRegistry(t, gw, store)
	after, err := restarted.List(context.Background())
	if err != nil {
		t.Fatalf("post-restart List: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("post-restart List returned %d views, want 1", len(after))
	}
	v := after[0]
	if v.Source != SourceReplayed {
		t.Fatalf("post-restart source = %q, want %q", v.Source, SourceReplayed)
	}
	if got, want := viewCore(v), viewCore(before[0]); got.ID != want.ID || got.Sandbox != want.Sandbox ||
		got.Status != want.Status || got.Session != want.Session ||
		got.Created == nil || want.Created == nil || !got.Created.Equal(*want.Created) {
		t.Fatalf("post-restart view %+v differs from pre-restart %+v", v, before[0])
	}
}

// TestReplayStatusFromLog pins the log-derived agent status: the CR phase
// alone no longer decides it. A log ending in agent.failed yields failed even
// though the sandbox is READY.
func TestReplayStatusFromLog(t *testing.T) {
	_, gw, stream, agent, store := newRegistryTestWithStore(t)

	stream.Exit(1)
	waitFor(t, func() bool { return agent.Status() == StatusFailed }, "pi exit → failed")

	restarted := restartRegistry(t, gw, store)
	v, err := restarted.GetView(context.Background(), agent.ID)
	if err != nil {
		t.Fatalf("GetView: %v", err)
	}
	if v.Status != StatusFailed || v.Source != SourceReplayed {
		t.Fatalf("view = %+v, want replayed failed", v)
	}
}

// TestReplaySkipsTerminated pins that a terminated session is not
// re-registered: the sandbox was deleted, so a post-restart get_agent stays
// ErrUnknownAgent (matching pre-restart behavior after Delete).
func TestReplaySkipsTerminated(t *testing.T) {
	mgr, gw, _, agent, store := newRegistryTestWithStore(t)

	if err := mgr.Delete(context.Background(), agent.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	restarted := restartRegistry(t, gw, store)
	if _, err := restarted.GetView(context.Background(), agent.ID); err == nil {
		t.Fatal("get_agent on terminated agent returned a view after restart, want unknown")
	}
}

// TestReplayHydratesHubForCursors pins the event-surface reachability AC:
// read_events after a cursor works across the restart (durable log, hub seq
// recovered with no gap).
func TestReplayHydratesHubForCursors(t *testing.T) {
	mgr, gw, stream, agent, store := newRegistryTestWithStore(t)

	if err := mgr.Prompt(agent.ID, "turn"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	stream.Deliver(`{"type":"agent_settled"}`)
	waitFor(t, func() bool { return agent.Status() == StatusReady }, "settle")
	full := agent.hub.Events()
	if len(full) < 2 {
		t.Fatalf("log too small to test a cursor: %d events", len(full))
	}
	cursor := full[0].Seq
	want := full[1:]

	restarted := restartRegistry(t, gw, store)
	got := restarted.Get(agent.ID)
	if got == nil {
		t.Fatal("agent not rehydrated after restart")
	}
	res := got.hub.Read(cursor, 0, nil)
	if res.Err != nil {
		t.Fatalf("Read: %v", res.Err)
	}
	if len(res.Events) != len(want) {
		t.Fatalf("replay after cursor %d = %d events, want %d", cursor, len(res.Events), len(want))
	}
	for i := range want {
		if res.Events[i].Seq != want[i].Seq || string(res.Events[i].Data) != string(want[i].Data) {
			t.Fatalf("event %d = %+v, want %+v", i, res.Events[i], want[i])
		}
	}
}

// TestReplayOwnershipJoinUnchanged pins AC 3: the ownership-label join is
// unchanged — foreign sandboxes are still excluded, and the replayed state
// only ever joins onto owned sandboxes.
func TestReplayOwnershipJoinUnchanged(t *testing.T) {
	_, gw, _, agent, store := newRegistryTestWithStore(t)

	gw.SeedSandbox("sbx-foreign", "uuid-f", map[string]string{LabelManagedBy: "someone-else", LabelAgentID: "agent-x"}, 0)
	gw.SeedSandbox("sbx-handmade", "uuid-h", map[string]string{}, 0)

	restarted := restartRegistry(t, gw, store)
	views, err := restarted.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 1 || views[0].ID != agent.ID || views[0].Source != SourceReplayed {
		t.Fatalf("views = %+v, want only the owned replayed view for %s", views, agent.ID)
	}
}

// TestReplayCRFallbackForLegacyLogs pins the demotion semantics: a log
// without the legacy-aware provisioning payload (no recorded sandbox name)
// still joins onto the CR for the sandbox name — CRs remain the address
// source.
func TestReplayCRFallbackForLegacyLogs(t *testing.T) {
	_, gw, _, agent, store := newRegistryTestWithStore(t)

	// Simulate a legacy log: strip the sandbox field by hand-appending a
	// bare lifecycle event through a fresh hub (no sandbox name recorded).
	hub, err := NewEventHub(store, "agent-legacy")
	if err != nil {
		t.Fatalf("NewEventHub: %v", err)
	}
	hub.PublishLifecycle("agent.provisioned", map[string]interface{}{"agent_id": "agent-legacy", "status": StatusCreating})
	hub.PublishLifecycle("agent.started", map[string]interface{}{"agent_id": "agent-legacy", "status": SessionReady})
	hub.Close()

	// Give the legacy agent a sandbox CR so the join can address it.
	gw.SeedSandbox("sbx-legacy", "uuid-legacy", map[string]string{LabelManagedBy: ManagedByValue, LabelAgentID: "agent-legacy"}, 0)
	_ = agent

	restarted := restartRegistry(t, gw, store)
	views, err := restarted.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, v := range views {
		if v.ID == "agent-legacy" {
			found = true
			if v.Sandbox != "sbx-legacy" {
				t.Fatalf("legacy view sandbox = %q, want CR-patched %q", v.Sandbox, "sbx-legacy")
			}
			if v.Source != SourceReplayed || v.Status != StatusReady {
				t.Fatalf("legacy view = %+v, want replayed ready", v)
			}
			if v.Created != nil {
				t.Fatal("legacy view must omit created (not in the log)")
			}
		}
	}
	if !found {
		t.Fatalf("legacy agent missing from views: %+v", views)
	}
}

// TestReplayPromptRejected pins that a rehydrated (replayed-only) agent has
// no live exec stream: prompt is rejected, not sent into a nil stdin.
func TestReplayPromptRejected(t *testing.T) {
	_, gw, _, agent, store := newRegistryTestWithStore(t)

	restarted := restartRegistry(t, gw, store)
	if err := restarted.Prompt(agent.ID, "turn"); err == nil {
		t.Fatal("prompt on replayed-only agent accepted, want rejection (no live exec stream)")
	}
}

// TestReplayAfterLiveRegistration pins the join precedence: a live agent
// (this process created it) wins over replayed state for the same id.
func TestReplayAfterLiveRegistration(t *testing.T) {
	mgr, _, _, _, _ := newRegistryTestWithStore(t)

	if err := mgr.RebuildFromLog(context.Background()); err != nil {
		t.Fatalf("RebuildFromLog: %v", err)
	}
	views, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 1 || views[0].Source != SourceLive {
		t.Fatalf("views = %+v, want live view winning over replayed", views)
	}
}

// newRegistryTestWithStore is newRegistryTest with the store/gateway handle
// exposed so replay tests can restart over the same store.
func newRegistryTestWithStore(t *testing.T) (*AgentManager, *FakeGateway, *FakeStream, *Agent, *BoltEventStore) {
	t.Helper()
	gw := NewFakeGateway()
	store := testStore(t)
	mgr := NewAgentManager(gw, testConfig(), store)
	stream := NewFakeStream()
	gw.SetStream(stream)
	agent, err := mgr.Create(context.Background(), &CreateProfile{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return mgr, gw, stream, agent, store
}
