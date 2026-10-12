package main

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// newSpawnManager returns a manager over a FakeGateway (exec transport) for
// the spawning tests (issue #216, ADR-0083 D8).
func newSpawnManager(t *testing.T) (*AgentManager, *FakeGateway) {
	t.Helper()
	gw := NewFakeGateway()
	gw.SetStream(NewFakeStream())
	mgr := NewAgentManager(gw, testConfig(), testStore(t))
	return mgr, gw
}

// TestCreateChildInheritsParentProfile verifies D8 profile inheritance: a
// child spawned with parent_id and no overrides gets the parent's resolved
// profile (image/resources/model); provider is always the deployment default
// (issue #220, option B — not inherited, not caller-set).
func TestCreateChildInheritsParentProfile(t *testing.T) {
	mgr, gw := newSpawnManager(t)
	parent, err := mgr.Create(context.Background(), &CreateProfile{
		Image:     "parent-image:v1",
		Model:     "z-ai/glm-5.2",
		Resources: &Resources{CPU: "2", Memory: "4Gi"},
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := mgr.Create(context.Background(), &CreateProfile{ParentID: parent.ID})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	reqs := gw.CreatedRequests()
	if len(reqs) != 2 {
		t.Fatalf("created %d sandboxes, want 2", len(reqs))
	}
	c := reqs[1]
	if c.Image != "parent-image:v1" || c.Provider != "openrouter-spike" || c.Resources != (Resources{CPU: "2", Memory: "4Gi"}) {
		t.Fatalf("child spec = %+v, want parent profile inherited with deployment-fixed provider", c)
	}
	if got := gw.PiModels()[1]; got != "z-ai/glm-5.2" {
		t.Fatalf("child model = %q, want parent model inherited", got)
	}
	if child.ParentID != parent.ID {
		t.Fatalf("child ParentID = %q, want %q", child.ParentID, parent.ID)
	}
}

// TestCreateChildOverridesParentProfile: inheritance is overridable — an
// explicitly set field wins over the parent's resolved value (matches the
// existing partial-profile Create semantics).
func TestCreateChildOverridesParentProfile(t *testing.T) {
	mgr, gw := newSpawnManager(t)
	parent, err := mgr.Create(context.Background(), &CreateProfile{
		Image: "parent-image:v1",
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := mgr.Create(context.Background(), &CreateProfile{
		ParentID: parent.ID,
		Image:    "child-image:v1",
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	reqs := gw.CreatedRequests()
	if reqs[1].Image != "child-image:v1" {
		t.Fatalf("child image = %q, want explicit override", reqs[1].Image)
	}
	if reqs[1].Provider != "openrouter-spike" {
		t.Fatalf("child provider = %q, want deployment-fixed default (issue #220, option B)", reqs[1].Provider)
	}
}

// TestAgentViewParentChildren verifies agent_view gains parent/children.
func TestAgentViewParentChildren(t *testing.T) {
	mgr, _ := newSpawnManager(t)
	parent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	c1, err := mgr.Create(context.Background(), &CreateProfile{ParentID: parent.ID})
	if err != nil {
		t.Fatalf("create child 1: %v", err)
	}
	c2, err := mgr.Create(context.Background(), &CreateProfile{ParentID: parent.ID})
	if err != nil {
		t.Fatalf("create child 2: %v", err)
	}

	pv, err := mgr.GetView(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("parent view: %v", err)
	}
	if pv.Parent != "" {
		t.Fatalf("parent view parent = %q, want empty (root)", pv.Parent)
	}
	if len(pv.Children) != 2 || !slices.Contains(pv.Children, c1.ID) || !slices.Contains(pv.Children, c2.ID) {
		t.Fatalf("parent view children = %v, want [%s %s]", pv.Children, c1.ID, c2.ID)
	}
	cv, err := mgr.GetView(context.Background(), c1.ID)
	if err != nil {
		t.Fatalf("child view: %v", err)
	}
	if cv.Parent != parent.ID {
		t.Fatalf("child view parent = %q, want %q", cv.Parent, parent.ID)
	}
	if len(cv.Children) != 0 {
		t.Fatalf("child view children = %v, want empty", cv.Children)
	}
}

// TestCreateChildUnknownParentRejected: an unknown parent_id is a validation
// error naming the field, raised BEFORE any gateway call (no sandbox leaked).
func TestCreateChildUnknownParentRejected(t *testing.T) {
	mgr, gw := newSpawnManager(t)
	_, err := mgr.Create(context.Background(), &CreateProfile{ParentID: "agent-nonexistent"})
	if err == nil {
		t.Fatal("create with unknown parent_id: want error")
	}
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "parent_id" {
		t.Fatalf("err = %v, want FieldError(parent_id)", err)
	}
	if len(gw.created) != 0 {
		t.Fatalf("gateway created %v, want none (no sandbox leaked)", gw.created)
	}
}

// TestSpawnDepthCapBeforeGatewayCall: SPAWN_MAX_DEPTH=2 means root (depth 0)
// → subagent (1) → sub-subagent (2); a third-level spawn is rejected before
// any gateway call.
func TestSpawnDepthCapBeforeGatewayCall(t *testing.T) {
	mgr, gw := newSpawnManager(t)
	root, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	sub, err := mgr.Create(context.Background(), &CreateProfile{ParentID: root.ID})
	if err != nil {
		t.Fatalf("create subagent: %v", err)
	}
	subsub, err := mgr.Create(context.Background(), &CreateProfile{ParentID: sub.ID})
	if err != nil {
		t.Fatalf("create sub-subagent: %v", err)
	}
	if subsub.Depth() != 2 {
		t.Fatalf("sub-subagent depth = %d, want 2", subsub.Depth())
	}
	_, err = mgr.Create(context.Background(), &CreateProfile{ParentID: subsub.ID})
	if !errors.Is(err, ErrSpawnDepthExceeded) {
		t.Fatalf("depth-3 spawn err = %v, want ErrSpawnDepthExceeded", err)
	}
	if len(gw.created) != 3 {
		t.Fatalf("gateway created %v, want 3 (rejection must not leak a sandbox)", gw.created)
	}
}

// TestSpawnChildrenCapBeforeGatewayCall: the 9th child of one parent is
// rejected before any gateway call.
func TestSpawnChildrenCapBeforeGatewayCall(t *testing.T) {
	mgr, gw := newSpawnManager(t)
	parent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := mgr.Create(context.Background(), &CreateProfile{ParentID: parent.ID}); err != nil {
			t.Fatalf("create child %d: %v", i+1, err)
		}
	}
	if _, err := mgr.Create(context.Background(), &CreateProfile{ParentID: parent.ID}); !errors.Is(err, ErrSpawnChildrenExceeded) {
		t.Fatalf("9th child err = %v, want ErrSpawnChildrenExceeded", err)
	}
	if len(gw.created) != 9 {
		t.Fatalf("gateway created %d sandboxes, want 9 (rejection must not leak a sandbox)", len(gw.created))
	}
}

// TestCascadeTerminateBottomUp: deleting a parent terminates the whole
// subtree, grandchildren first (bottom-up), each child's sandbox deleted
// before its parent's.
func TestCascadeTerminateBottomUp(t *testing.T) {
	mgr, gw := newSpawnManager(t)
	root, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	c1, err := mgr.Create(context.Background(), &CreateProfile{ParentID: root.ID})
	if err != nil {
		t.Fatalf("create child 1: %v", err)
	}
	gc, err := mgr.Create(context.Background(), &CreateProfile{ParentID: c1.ID})
	if err != nil {
		t.Fatalf("create grandchild: %v", err)
	}
	c2, err := mgr.Create(context.Background(), &CreateProfile{ParentID: root.ID})
	if err != nil {
		t.Fatalf("create child 2: %v", err)
	}
	if err := mgr.Delete(context.Background(), root.ID); err != nil {
		t.Fatalf("delete root: %v", err)
	}

	for _, a := range []*Agent{root, c1, c2, gc} {
		if got := a.Status(); got != StatusTerminated {
			t.Fatalf("agent %s status = %q, want terminated", a.ID, got)
		}
		if mgr.Get(a.ID) != nil {
			t.Fatalf("agent %s still registered", a.ID)
		}
	}
	// Bottom-up ordering: the grandchild's sandbox is deleted before its
	// parent's, and every child before the root's.
	idx := func(name string) int { return slices.Index(gw.deleted, name) }
	if idx(gc.Sandbox) >= idx(c1.Sandbox) {
		t.Fatalf("delete order %v: grandchild %s must precede child %s", gw.deleted, gc.Sandbox, c1.Sandbox)
	}
	if idx(c1.Sandbox) >= idx(root.Sandbox) {
		t.Fatalf("delete order %v: child %s must precede root %s", gw.deleted, c1.Sandbox, root.Sandbox)
	}
	if idx(c2.Sandbox) >= idx(root.Sandbox) {
		t.Fatalf("delete order %v: child %s must precede root %s", gw.deleted, c2.Sandbox, root.Sandbox)
	}
}

// TestCascadeDetachesChildrenIDsOnParentView: after a child dies via cascade
// the parent view must not keep listing terminated children (the parent is
// terminated here too, but a standalone child Delete must detach it).
func TestStandaloneChildDeleteDetachesFromParent(t *testing.T) {
	mgr, _ := newSpawnManager(t)
	parent, err := mgr.Create(context.Background(), nil)
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := mgr.Create(context.Background(), &CreateProfile{ParentID: parent.ID})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := mgr.Delete(context.Background(), child.ID); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	pv, err := mgr.GetView(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("parent view: %v", err)
	}
	if slices.Contains(pv.Children, child.ID) {
		t.Fatalf("parent view children = %v, want detached (no terminated child)", pv.Children)
	}
}

// TestSpawnCapsFromEnvAndFlags: SPAWN_MAX_DEPTH / SPAWN_MAX_CHILDREN are
// controller config (env then flag, flag > env > default).
func TestSpawnCapsConfig(t *testing.T) {
	t.Setenv("KAPE_SPAWN_MAX_DEPTH", "3")
	t.Setenv("KAPE_SPAWN_MAX_CHILDREN", "5")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.SpawnMaxDepth != 3 || cfg.SpawnMaxChildren != 5 {
		t.Fatalf("env caps = depth %d children %d, want 3/5", cfg.SpawnMaxDepth, cfg.SpawnMaxChildren)
	}
	cfg = DefaultConfig()
	fs := newTestFlagSet()
	cfg.RegisterFlags(fs)
	if err := fs.Parse([]string{"-spawn-max-depth", "4", "-spawn-max-children", "9"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if cfg.SpawnMaxDepth != 4 || cfg.SpawnMaxChildren != 9 {
		t.Fatalf("flag caps = depth %d children %d, want 4/9", cfg.SpawnMaxDepth, cfg.SpawnMaxChildren)
	}
}

// TestMCPCreateAgentParentID: the MCP create_agent tool accepts the optional
// parent_id input and returns the child view with parent linkage.
func TestMCPCreateAgentParentID(t *testing.T) {
	cs, mgr, _, _ := newMCPClientSession(t)
	parent := mustCreateAgent(t, mgr)
	child := callTool[CreateProfile, AgentView](t, cs, "create_agent", CreateProfile{ParentID: parent.ID})
	if child.Parent != parent.ID {
		t.Fatalf("MCP child view parent = %q, want %q", child.Parent, parent.ID)
	}
}
