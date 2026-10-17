package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	v1 "github.com/dzungtr/kape/controller/gen"
)

// AgentStatus is the AgentManager FSM state, derived purely from
// controller-observed facts (sandbox phase polls, exec stream state, prompt
// bookkeeping) — never set by callers.
type AgentStatus string

const (
	// StatusCreating: sandbox provisioning (create submitted, waiting READY).
	StatusCreating AgentStatus = "creating"
	// StatusReady: sandbox READY and pi exec stream open, no turn in flight.
	StatusReady AgentStatus = "ready"
	// StatusWorking: a prompt was accepted and pi has not settled yet.
	StatusWorking AgentStatus = "working"
	// StatusFailed: pi non-zero exit, exec stream break, or sandbox ERROR phase.
	StatusFailed AgentStatus = "failed"
	// StatusTerminated: agent deleted.
	StatusTerminated AgentStatus = "terminated"
)

// piSettledEventType is the pi RPC record that marks the end of a turn: after
// agent_settled, the agent is idle and may accept another prompt.
const piSettledEventType = "agent_settled"

// textDeltaEventType is the pi RPC record type carrying streamed answer
// tokens. The MCP read_events tool excludes it by default (include_deltas
// opt-in) so host-agent context grows per turn, not per token.
const textDeltaEventType = "text_delta"

// piAbortCommand is the pi RPC abort command (verified against local pi
// 0.87.1 docs, @earendil-works/pi-coding-agent docs/rpc-commands.md §abort):
// a bare object with no id field; pi replies {"type":"response",
// "command":"abort","success":true} once the session is idle again.
type piAbortCommand struct {
	Type string `json:"type"` // "abort"
}

// Agent is one sandbox agent instance: a sandbox plus one pi RPC process
// bound to a single long-lived exec stream (the M1 connection-bound lifetime
// property, carried into v1 until the bridge daemon replaces it).
type Agent struct {
	ID      string    `json:"id"`
	Sandbox string    `json:"sandbox"`
	Created time.Time `json:"created"`
	// ParentID is the spawning parent (issue #216, D8): "" for a root agent.
	// Restored from the durable provisioned event on replay, so depth caps
	// and cascade termination survive a controller restart.
	ParentID string
	// resolved is the profile this agent was created with (fully resolved);
	// the inheritance source for spawned children. Empty for replayed agents
	// whose log predates the profile payload — their children fall back to
	// config defaults.
	resolved ResolvedProfile

	mu     sync.Mutex
	status AgentStatus

	sandboxID string // gateway sandbox UUID (used by exec RPCs)
	piSeq     int    // last pi RPC prompt id issued

	// replayedOnly marks an agent rebuilt from the durable log at startup
	// (issue #197): registry + hub are hydrated, but no exec stream exists
	// in this process — prompts/aborts are rejected, view source is
	// "replayed".
	replayedOnly bool

	// bootID is the last observed daemon boot identity (issue #213, D6):
	// learned by the puller's reconnect preflight, compared to detect daemon
	// death (new boot_id + pi_running:false). In-memory only.
	bootID string
	// degraded is the transient liveness flag (issue #213, D6): set after
	// degradedFailureThreshold consecutive pull failures, cleared by any
	// successful pull. Surfaced on agent_view, never durable.
	degraded bool

	// reviveMu serializes the Tier-2 revive ledger decision per agent
	// (issue #251): pause/spend, replay revival, and re-arm never interleave.
	reviveMu sync.Mutex

	// pendingRevive is the Tier-2 revive completion flag (issue #251): set
	// when a paused session's replacement daemon is up, consumed by the
	// puller's next successful pull (daemon.boot + first pull → paused →
	// running). In-memory only: a controller restart re-drives the revive
	// from the paused replay state (revive.go).
	pendingRevive bool

	hub   *EventHub   // buffered + fan-out of raw JSONL records from pi stdout
	fsm   *SessionFSM // session lifecycle projection over the log (issue #196)
	stdin chan []byte // exec break-glass path only; nil under the pull transport
	// daemon is the pull-transport client (issues #212/#214): the per-agent
	// puller drains its spool into the hub and commands ride POST /commands.
	// It is built lazily via the resolver and dropped on pull failure, so a
	// reconnect re-resolves the address from the Sandbox CR (podIPs churn on
	// pod restart). pullTransport marks the pull path (daemon may be nil).
	daemonMu sync.Mutex
	daemon   DaemonClient

	pullTransport bool
	cmdSeq        int // monotonic per-agent daemon command_id counter (pull path)
	cancel        context.CancelFunc
	gw            GatewayClient
	mgr           *AgentManager
}

// Status returns the agent's current FSM status.
func (a *Agent) Status() AgentStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// setStatusAndEmit publishes a lifecycle event for status changes worth
// surfacing on the event stream.
func (a *Agent) setStatusAndEmit(s AgentStatus, event string) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	a.hub.PublishLifecycle(event, map[string]interface{}{
		"agent_id": a.ID,
		"status":   s,
	})
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// EventHub — the seq-aware replay buffer and fan-out — lives in events.go.

// AgentManager tracks agents in memory and owns the per-agent FSM:
// creating → ready ⇄ working, and → failed | terminated. The gateway client
// is behind GatewayClient so FSM tests run against a fake.
type AgentManager struct {
	mu     sync.Mutex
	agents map[string]*Agent
	gw     GatewayClient
	cfg    Config
	// managedBy is the value stamped into the managed-by ownership label and
	// used to filter ListSandboxes — the registry's identity.
	managedBy string
	// store is the durable event log shared by every agent's hub (issue #195):
	// one controller-owned file, one writer per agent bucket.
	store EventStore
	// daemon is the shared pull-transport client — TEST SEAM only: when set,
	// daemonFor hands it out to every agent instead of resolving addresses
	// (production builds never set it; issue #214 removed static addressing).
	daemon DaemonClient
	// daemonTransport is the test HTTP transport handed to resolved clients.
	daemonTransport http.RoundTripper
	// resolver resolves the per-agent daemon address from the Sandbox CR
	// (issue #214, D7). Set in main when transport is pull.
	resolver DaemonAddressResolver
	// monitor is the SLM monitor loop (issue #232): armed on turn_started,
	// disarmed on settle/termination/liveness failure, terminal assessment
	// in the settle path. Nil (config-disabled) → every hook is a no-op.
	monitor *Monitor
	// now is the clock seam for the Tier-2 revive ledger cooldown (issue
	// #251); tests inject a fake clock.
	now func() time.Time
}

func NewAgentManager(gw GatewayClient, cfg Config, store EventStore) *AgentManager {
	return &AgentManager{agents: map[string]*Agent{}, gw: gw, cfg: cfg, managedBy: ManagedByValue, store: store, now: time.Now}
}

// WithResolver sets the daemon address resolver (issue #214, D7).
func (m *AgentManager) WithResolver(r DaemonAddressResolver) *AgentManager {
	m.resolver = r
	return m
}

// WithMonitor attaches the SLM monitor loop (issue #232). A nil monitor
// (disabled config) keeps every hook a nil-receiver no-op.
func (m *AgentManager) WithMonitor(mo *Monitor) *AgentManager {
	m.monitor = mo
	return m
}

// daemonFor returns the agent's pull-transport client, building it from the
// Sandbox CR-resolved address on first use. Callers drop the client via
// invalidateDaemon on failure so the next reconnect re-resolves from the CR
// (podIPs churn on pod restart, D7).
func (m *AgentManager) daemonFor(ctx context.Context, a *Agent) (DaemonClient, error) {
	a.daemonMu.Lock()
	dc := a.daemon
	a.daemonMu.Unlock()
	if dc != nil {
		return dc, nil
	}
	if m.daemon != nil {
		return m.daemon, nil // test seam: static shared client
	}
	if m.resolver == nil {
		return nil, fmt.Errorf("no daemon address resolver configured")
	}
	base, err := m.resolver.ResolveDaemonAddr(ctx, a.Sandbox)
	if err != nil {
		return nil, err
	}
	dc = NewHTTPDaemonClient(base, m.cfg.DaemonToken)
	if m.daemonTransport != nil {
		dc.(*HTTPDaemonClient).hc.Transport = m.daemonTransport
	}
	a.daemonMu.Lock()
	if a.daemon == nil {
		a.daemon = dc
	}
	a.daemonMu.Unlock()
	return dc, nil
}

// invalidateDaemon drops the cached daemon client so the next use re-resolves
// the address from the Sandbox CR.
func (m *AgentManager) invalidateDaemon(a *Agent) {
	a.daemonMu.Lock()
	a.daemon = nil
	a.daemonMu.Unlock()
}

// Create provisions a sandbox for the (possibly partial) profile override
// set, waits for READY, applies the model-gateway egress policy, then opens
// the long-lived pi exec stream. Validation happens BEFORE any gateway call,
// so a rejected create never leaks a sandbox. On later (gateway) failure the
// sandbox is deleted and an error returned. On success the agent is
// StatusReady; a phase-poll goroutine watches for ERROR phases and the data
// plane — the daemon puller (default, issue #212) or the pi-pump exec stream
// (break-glass) — runs for the agent's lifetime.
func (m *AgentManager) Create(ctx context.Context, profile *CreateProfile) (*Agent, error) {
	if profile == nil {
		profile = &CreateProfile{}
	}
	parent, err := m.resolveParent(ctx, profile.ParentID)
	if err != nil {
		return nil, err
	}
	// Caps BEFORE any gateway call (D8): a rejected spawn never leaks a
	// sandbox.
	if err := m.checkSpawnCaps(ctx, profile.ParentID, parent); err != nil {
		return nil, err
	}
	if parent != nil {
		inheritProfile(profile, parent.resolved)
	}
	resolved, err := m.cfg.ResolveProfile(profile)
	if err != nil {
		return nil, err
	}
	id := randID("agent")
	if resolved.Name == "" {
		// The OpenShell gateway caps sandbox names at 19 chars, so the
		// derived default drops the "agent-" prefix: sbx-<hex> (16 chars).
		resolved.Name = "sbx-" + id[len("agent-"):]
	}
	name := resolved.Name
	hub, err := NewEventHub(m.store, id)
	if err != nil {
		return nil, fmt.Errorf("open event log for %s: %w", id, err)
	}
	agent := &Agent{ID: id, Sandbox: name, Created: time.Now(), hub: hub, fsm: NewSessionFSM(hub, id, SessionProvision), gw: m.gw, mgr: m, ParentID: profile.ParentID, resolved: resolved}

	agent.mu.Lock()
	agent.status = StatusCreating
	agent.mu.Unlock()
	log.Printf("[agent %s] creating sandbox %s (image %s, provider %s, cpu %s, memory %s, model %q)",
		id, name, resolved.Image, resolved.Provider, resolved.Resources.CPU, resolved.Resources.Memory, resolved.Model)

	labels := map[string]string{
		LabelManagedBy: m.managedBy, // ownership tag: the label registry's filter key
		LabelAgentID:   id,          // agent-id: resolves sandbox → agent after restart
	}
	if parent != nil {
		// Durable parent linkage (issue #216): drives the children cap count,
		// CR-derived views, and post-restart cascade termination.
		labels[LabelParentAgentID] = parent.ID
	}
	_, sbID, err := m.gw.CreateSandbox(ctx, name, resolved.Image, resolved.Provider, resolved.Resources, labels, PiSessionID(id))
	if err != nil {
		return nil, err
	}
	agent.sandboxID = sbID
	if err := m.waitReady(ctx, agent); err != nil {
		_ = m.gw.DeleteSandbox(context.Background(), name)
		return nil, err
	}
	log.Printf("[agent %s] sandbox %s READY", id, name)
	// created and sandbox land in the log so a restart rebuilds the view
	// from replay alone (issue #197); the sandbox CR stays the address
	// source — this payload only lets the replayed view echo it.
	prov := map[string]interface{}{
		"agent_id": agent.ID,
		"status":   StatusCreating,
		"sandbox":  agent.Sandbox,
		"created":  agent.Created.UTC().Format(time.RFC3339Nano),
		// Profile payload (issue #216): lets replay reconstruct the resolved
		// profile so spawned children inherit it across a controller restart.
		"image":    resolved.Image,
		"provider": resolved.Provider,
		"model":    resolved.Model,
		"cpu":      resolved.Resources.CPU,
		"memory":   resolved.Resources.Memory,
	}
	if parent != nil {
		prov["parent"] = parent.ID
	}
	agent.hub.PublishLifecycle("agent.provisioned", prov)

	if err := m.gw.ApplyModelGatewayPolicy(ctx, name); err != nil {
		_ = m.gw.DeleteSandbox(context.Background(), name)
		return nil, fmt.Errorf("apply network policy: %w", err)
	}
	// Structural sandbox→controller egress deny (issue #214, ADR-0083 D2/D7):
	// the daemon never dials the controller; the deny takes precedence over
	// any future allow rule.
	if err := m.gw.ApplyControllerEgressDeny(ctx, name, m.cfg.ControllerServiceHost, m.cfg.ControllerServicePort); err != nil {
		_ = m.gw.DeleteSandbox(context.Background(), name)
		return nil, fmt.Errorf("apply controller egress deny: %w", err)
	}

	if m.cfg.Transport == TransportPull {
		// Pull transport (issues #212/#214): the per-agent puller owns the
		// data plane and resolves the daemon address from the Sandbox CR on
		// every reconnect; commands ride POST /commands through the same client.
		ctx, cancel := context.WithCancel(context.Background())
		agent.cancel = cancel
		agent.pullTransport = true
		// Learn the daemon's boot identity BEFORE the puller races against
		// the world (issue #213 D6): the first boot_id comparison needs a
		// known baseline. The preflight dials the daemonFor-resolved client
		// (issue #214 D7 — the address comes from the Sandbox CR); an
		// unreachable daemon is tolerated — the puller's startup preflight
		// retries.
		m.noteBootID(agent, ctx)
		go m.phasePoll(agent, ctx)
		go m.runPuller(agent, ctx)
	} else if err := m.startPi(agent, resolved.Model); err != nil { // break-glass exec path
		_ = m.gw.DeleteSandbox(context.Background(), name)
		return nil, err
	}

	m.mu.Lock()
	m.agents[id] = agent
	m.mu.Unlock()

	if err := agent.fsm.Transition(SessionReady, "agent.started", nil); err != nil {
		// The agent is already registered: remove the zombie entry, stop the
		// pumps, and close the hub before returning (stdin is nil under the
		// pull transport).
		if agent.cancel != nil {
			agent.cancel()
		}
		agent.mu.Lock()
		agent.status = StatusTerminated
		if agent.stdin != nil {
			close(agent.stdin)
		}
		agent.mu.Unlock()
		agent.hub.Close()
		m.mu.Lock()
		delete(m.agents, id)
		m.mu.Unlock()
		_ = m.gw.DeleteSandbox(context.Background(), name)
		return nil, fmt.Errorf("record session started for %s: %w", id, err)
	}
	// The session event is already appended by fsm.Transition; only flip the
	// in-memory status, under the lock (phasePoll/stdoutPump read it).
	agent.mu.Lock()
	agent.status = StatusReady
	agent.mu.Unlock()
	return agent, nil
}

// PiSessionID derives the session-pinned pi --session-id from the durable
// session's agent id (spec #245 E3, issue #249): deterministic, so any
// replacement sandbox for the same session (Tier-2 revive, #251) threads the
// same id and pi resumes the session tree on the PVC. Agent ids stay
// per-instance naming; the pi id is the session-scoped resume key.
func PiSessionID(agentID string) string {
	return "kape-" + strings.TrimPrefix(agentID, "agent-")
}

// resolveParent validates the optional parent_id (issue #216 D8) and returns
// the registered parent, nil for a root create. Unknown or already-terminated
// parents are a field-naming validation error — raised BEFORE any gateway
// call, so a rejected spawn never leaks a sandbox.
func (m *AgentManager) resolveParent(ctx context.Context, parentID string) (*Agent, error) {
	if parentID == "" {
		return nil, nil
	}
	parent := m.Get(parentID)
	if parent == nil || parent.Status() == StatusTerminated {
		return nil, fieldErr("parent_id", fmt.Sprintf("unknown parent agent %q", parentID))
	}
	return parent, nil
}

// checkSpawnCaps enforces the D8 caps against the parent's current linkage:
// SPAWN_MAX_DEPTH (root=0, subagent=1, sub-subagent=2 by default) and
// SPAWN_MAX_CHILDREN per parent. Called before any gateway call.
func (m *AgentManager) checkSpawnCaps(ctx context.Context, parentID string, parent *Agent) error {
	if parent == nil {
		return nil
	}
	if parent.Depth()+1 > m.cfg.SpawnMaxDepth {
		return fmt.Errorf("%w: spawning under %s would exceed SPAWN_MAX_DEPTH=%d", ErrSpawnDepthExceeded, parentID, m.cfg.SpawnMaxDepth)
	}
	if n := m.childCount(ctx, parentID); n >= m.cfg.SpawnMaxChildren {
		return fmt.Errorf("%w: parent %s already has %d children (SPAWN_MAX_CHILDREN=%d)", ErrSpawnChildrenExceeded, parentID, n, m.cfg.SpawnMaxChildren)
	}
	return nil
}

// childCount counts the parent's children: the sandbox CRs are the durable
// linkage source (parent-agent-id label), with the in-memory map as fallback
// if the gateway listing fails.
func (m *AgentManager) childCount(ctx context.Context, parentID string) int {
	infos, err := m.gw.ListSandboxes(ctx, LabelParentAgentID+"="+parentID)
	if err == nil {
		n := 0
		for _, info := range infos {
			if m.owned(info) {
				n++
			}
		}
		return n
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, a := range m.agents {
		if a.ParentID == parentID {
			n++
		}
	}
	return n
}

// Depth returns the agent's spawning depth: the number of ancestors in the
// parent chain (root = 0). A dangling parent link ends the walk.
func (a *Agent) Depth() int {
	depth := 0
	seen := map[string]bool{a.ID: true}
	cur := a
	for cur.ParentID != "" {
		if seen[cur.ParentID] {
			return depth // corrupt-linkage guard; no infinite loop
		}
		seen[cur.ParentID] = true
		parent := a.mgr.Get(cur.ParentID)
		if parent == nil {
			return depth
		}
		cur = parent
		depth++
	}
	return depth
}

// inheritProfile applies the D8 inheritance rule to the child's override
// set: omitted fields take the parent's resolved value (Name excluded —
// sandbox names must be unique). Explicit child fields win, matching the
// existing partial-profile Create semantics.
func inheritProfile(profile *CreateProfile, parent ResolvedProfile) {
	if profile.Image == "" {
		profile.Image = parent.Image
	}
	if profile.Model == "" {
		profile.Model = parent.Model
	}
	if profile.Resources == nil {
		r := parent.Resources
		profile.Resources = &r
	}
}

// waitReady polls the sandbox phase until READY; ERROR is surfaced as failure.
func (m *AgentManager) waitReady(ctx context.Context, agent *Agent) error {
	deadline := time.Now().Add(10 * time.Minute)
	for {
		phase, err := m.gw.GetSandboxPhase(ctx, agent.Sandbox)
		if err != nil {
			return fmt.Errorf("GetSandbox(%s): %w", agent.Sandbox, err)
		}
		switch phase {
		case v1.SandboxPhase_SANDBOX_PHASE_READY:
			return nil
		case v1.SandboxPhase_SANDBOX_PHASE_ERROR:
			return fmt.Errorf("sandbox %s reached terminal phase %s", agent.Sandbox, phase)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for sandbox %s to be READY", agent.Sandbox)
		}
		time.Sleep(2 * time.Second)
	}
}

// Get returns an agent or nil.
func (m *AgentManager) Get(id string) *Agent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.agents[id]
}

// Delete terminates the agent and cascades to its spawned subtree
// (issue #216, D8): children — and grandchildren, recursively — terminate
// bottom-up, each child's sandbox deleted before its parent's. A standalone
// child delete (no cascade) detaches it from the parent view.
func (m *AgentManager) Delete(ctx context.Context, id string) error {
	return m.deleteCascade(ctx, id, map[string]bool{})
}

// deleteCascade deletes descendants first (bottom-up), then the agent itself.
// CR-only children (a registry whose replay lost their log) are torn down by
// direct sandbox deletion — they are not registered, so there is nothing
// else to terminate.
func (m *AgentManager) deleteCascade(ctx context.Context, id string, visited map[string]bool) error {
	if visited[id] {
		return nil // corrupt-linkage guard (cycle in the parent chain)
	}
	visited[id] = true
	m.mu.Lock()
	var childIDs []string
	for aid, a := range m.agents {
		if a.ParentID == id {
			childIDs = append(childIDs, aid)
		}
	}
	m.mu.Unlock()
	sort.Strings(childIDs) // deterministic bottom-up order
	var firstErr error
	for _, cid := range childIDs {
		if err := m.deleteCascade(ctx, cid, visited); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// Children whose log is gone (CR-derived only): delete their sandbox
	// directly so a parent delete never orphans a live sandbox.
	if infos, err := m.gw.ListSandboxes(ctx, LabelParentAgentID+"="+id); err == nil {
		for _, info := range infos {
			if m.owned(info) && m.Get(info.Labels[LabelAgentID]) == nil {
				_ = m.gw.DeleteSandbox(ctx, info.Name)
			}
		}
	}
	if err := m.deleteOne(ctx, id); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// deleteOne aborts the exec stream, deletes the sandbox, removes the agent —
// the single-agent teardown, no cascade.
func (m *AgentManager) deleteOne(ctx context.Context, id string) error {
	m.mu.Lock()
	agent := m.agents[id]
	delete(m.agents, id)
	m.mu.Unlock()
	if agent == nil {
		return errUnknownAgent(id)
	}
	if agent.cancel != nil {
		agent.cancel()
	}
	// Flip to terminal status and close stdin in ONE critical section:
	// any sender (sendPrompt, Abort) holds agent.mu while checking status
	// and sending, so it either sees pre-termination status with the
	// channel still open, or post-termination status with no send. Closing
	// before the status flip would allow a send on the closed channel —
	// a panic (send on closed channel). Emit the lifecycle event after
	// releasing the lock.
	agent.mu.Lock()
	agent.status = StatusTerminated
	if agent.stdin != nil {
		close(agent.stdin)
	}
	agent.mu.Unlock()
	if err := agent.fsm.Transition(SessionTerminated, "agent.terminated", nil); err != nil {
		log.Printf("[agent %s] record session terminated failed: %v", id, err)
	}
	// Termination disarms the monitor timer (issue #232).
	m.monitor.Disarm(id)
	agent.hub.Close()
	if err := m.gw.DeleteSandbox(ctx, agent.Sandbox); err != nil {
		return err
	}
	log.Printf("[agent %s] sandbox %s deleted", id, agent.Sandbox)
	return nil
}

// Prompt accepts a work item. Enforces the single in-flight prompt policy:
// a second prompt while a turn is in flight is rejected (HTTP 409 at the
// transport). No queueing in v1.
func (m *AgentManager) Prompt(id, message string) error {
	agent := m.Get(id)
	if agent == nil {
		return errUnknownAgent(id)
	}
	return agent.sendPrompt(message)
}

// sendPrompt enforces the single in-flight prompt policy from the SESSION
// FSM state (issue #196): a second prompt while the session is running is
// rejected (HTTP 409 at the transport); ready and idle accept.
//
// Pull transport (issue #212, ADR-0083 D4): the 409-equivalent is enforced
// controller-side BEFORE the POST — the daemon never sees a rejected prompt.
// An accepted prompt rides POST /commands as
// {command_id, type: "prompt", payload: {"message"}} — 202 on acceptance —
// then the ready/idle → running transition appends its lifecycle event.
//
// Exec break-glass path: the pi RPC prompt command
// ({"id","type":"prompt","message"} — verified against local pi docs,
// docs/rpc-commands.md §prompt) is written to the exec stdin before the
// transition.
func (a *Agent) sendPrompt(message string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pullTransport {
		// Pull transport: the daemon is the command path; there is no stdin.
		// The FSM gate comes FIRST (fail-fast, spec #245 E5): a paused session
		// (Tier-2 revive in flight or exhausted, issue #251) rejects before any
		// daemon resolution or POST — no command is queued behind a dead turn.
		switch a.fsm.State() {
		case SessionRunning:
			return errTurnInFlight(a.ID)
		case SessionReady, SessionIdle:
			if a.status != StatusReady {
				return errNotReady(a.ID, a.status)
			}
		default:
			return errNotReady(a.ID, a.status)
		}
		dc, err := a.mgr.daemonFor(context.Background(), a)
		if err != nil {
			return fmt.Errorf("resolve daemon address for %s: %w", a.Sandbox, err)
		}
		a.cmdSeq++
		payload, _ := json.Marshal(map[string]string{"message": message})
		cmd := DaemonCommand{ID: randID("cmd"), Type: "prompt", Payload: payload}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := dc.Command(ctx, cmd); err != nil {
			// Mirror the puller (D7): a command failure drops the cached
			// client so the next prompt re-resolves the address from the
			// Sandbox CR instead of re-dialing a stale podIP.
			a.mgr.invalidateDaemon(a)
			return fmt.Errorf("daemon rejected prompt: %w", err)
		}
		if err := a.fsm.Transition(SessionRunning, "agent.turn_started", map[string]interface{}{"prompt": message}); err != nil {
			// The 202 landed but the transition did not make the log; surface
			// it — the turn boundary is no longer derivable from replay alone.
			log.Printf("[agent %s] record turn_started failed: %v", a.ID, err)
		}
		a.status = StatusWorking
		// The monitor timer is armed on the turn-start transition (issue #232):
		// derived from the registry transition, never persisted.
		a.mgr.monitor.Arm(a.ID, a.hub)
		log.Printf("[agent %s] prompt accepted via daemon: %q", a.ID, message)
		return nil
	}
	if a.stdin == nil {
		// Replayed-only agent (issue #197): no exec stream in this process.
		return errNotReady(a.ID, a.status)
	}
	switch a.fsm.State() {
	case SessionRunning:
		return errTurnInFlight(a.ID)
	case SessionReady, SessionIdle:
		// accepted below, but only for a live, non-failed agent —
		// failures are an agent-liveness condition the session vocabulary
		// does not model (issue #196).
		if a.status != StatusReady {
			return errNotReady(a.ID, a.status)
		}
	default:
		return errNotReady(a.ID, a.status)
	}
	a.piSeq++
	cmd := map[string]interface{}{
		"id":      fmt.Sprintf("p%d", a.piSeq),
		"type":    "prompt",
		"message": message,
	}
	raw, _ := json.Marshal(cmd)
	select {
	case a.stdin <- append(raw, '\n'):
		if err := a.fsm.Transition(SessionRunning, "agent.turn_started", map[string]interface{}{"prompt": message}); err != nil {
			// The pi command was sent but the transition did not land in
			// the log; surface it — the turn boundary is no longer
			// derivable from replay alone.
			log.Printf("[agent %s] record turn_started failed: %v", a.ID, err)
		}
		a.status = StatusWorking
		// The monitor timer is armed on the turn-start transition (issue #232).
		a.mgr.monitor.Arm(a.ID, a.hub)
		log.Printf("[agent %s] prompt accepted: %q", a.ID, message)
		return nil
	default:
		return fmt.Errorf("stdin queue full")
	}
}

// Steer injects a mid-turn steering message into the in-flight turn
// (issue #215, ADR-0083 D4, pi's steer semantics). Steering is only
// meaningful while a turn is in flight: an idle agent is rejected with the
// 409-equivalent (ErrNotReady) before any POST.
func (m *AgentManager) Steer(id, message string) error {
	agent := m.Get(id)
	if agent == nil {
		return errUnknownAgent(id)
	}
	return agent.sendSteer(message)
}

// sendSteer enforces the mid-turn policy: only a running session with a
// working agent accepts a steer. Unlike prompt, steer never changes the FSM
// state — the turn already runs; settle flows via the records stream.
//
// Pull transport (issue #215): the steer rides POST /commands as
// {command_id, type: "steer", payload: {"message"}} — 202 on acceptance.
// The idle rejection is controller-side, before the POST (D4). Exec
// break-glass path: the pi RPC steer command ({"id","type":"steer",
// "message"} — same shape as prompt) is written to the exec stdin.
func (a *Agent) sendSteer(message string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pullTransport {
		// Pull transport: the daemon is the command path; there is no stdin.
		if a.fsm.State() != SessionRunning || a.status != StatusWorking {
			return errNotReady(a.ID, a.status) // idle: nothing to steer (409-equivalent)
		}
		dc, err := a.mgr.daemonFor(context.Background(), a)
		if err != nil {
			return fmt.Errorf("resolve daemon address for %s: %w", a.Sandbox, err)
		}
		a.cmdSeq++
		payload, _ := json.Marshal(map[string]string{"message": message})
		cmd := DaemonCommand{ID: randID("cmd"), Type: "steer", Payload: payload}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := dc.Command(ctx, cmd); err != nil {
			return fmt.Errorf("daemon rejected steer: %w", err)
		}
		log.Printf("[agent %s] steer accepted via daemon: %q", a.ID, message)
		return nil
	}
	if a.stdin == nil {
		return errNotReady(a.ID, a.status) // fast path (also replayed-only)
	}
	if a.fsm.State() != SessionRunning || a.status != StatusWorking {
		return errNotReady(a.ID, a.status)
	}
	a.piSeq++
	cmd := map[string]interface{}{
		"id":      fmt.Sprintf("s%d", a.piSeq),
		"type":    "steer",
		"message": message,
	}
	raw, _ := json.Marshal(cmd)
	select {
	case a.stdin <- append(raw, '\n'):
		log.Printf("[agent %s] steer accepted: %q", a.ID, message)
		return nil
	default:
		return fmt.Errorf("stdin queue full")
	}
}

// Abort stops the in-flight turn. Pull transport (issue #212, ADR-0083 D4):
// POST {command_id, type: "abort"} to the daemon — 202 on acceptance. Exec
// break-glass path: the pi RPC abort command ({"type":"abort"}) is written
// to the exec stdin, which stays open: pi settles the turn and subsequent
// records still reach the event stream, flipping the agent back to ready on
// the settled record.
func (m *AgentManager) Abort(id string) error {
	agent := m.Get(id)
	if agent == nil {
		return errUnknownAgent(id)
	}
	if agent.Status() != StatusWorking {
		return errNotReady(id, agent.Status())
	}
	if agent.pullTransport {
		// Pull transport: abort rides POST /commands (D4). The daemon never
		// tracks settlement — the turn boundary flows via the records stream.
		agent.mu.Lock()
		defer agent.mu.Unlock()
		if agent.status != StatusWorking {
			return errNotReady(id, agent.status)
		}
		agent.cmdSeq++
		cmd := DaemonCommand{ID: randID("cmd"), Type: "abort"}
		// daemonFor also re-resolves the address when the cached client was
		// dropped after a pull failure (D7).
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		dc, err := agent.mgr.daemonFor(ctx, agent)
		if err != nil {
			return fmt.Errorf("resolve daemon address for %s: %w", agent.Sandbox, err)
		}
		if _, err := dc.Command(ctx, cmd); err != nil {
			// Mirror the puller (D7): a command failure drops the cached
			// client so the next abort re-resolves the address from the
			// Sandbox CR instead of re-dialing a stale podIP.
			agent.mgr.invalidateDaemon(agent)
			return fmt.Errorf("daemon rejected abort: %w", err)
		}
		log.Printf("[agent %s] abort accepted via daemon", id)
		return nil
	}
	if agent.stdin == nil {
		return errNotReady(id, agent.Status()) // fast path (also replayed-only)
	}
	raw, _ := json.Marshal(piAbortCommand{Type: "abort"})
	// Hold the agent lock for the status re-check and the send together:
	// Delete flips to terminated and closes stdin in one critical
	// section, so re-checking under the lock is what prevents a send on
	// the closed channel (a TOCTOU panic if only the pre-lock check ran).
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.status != StatusWorking {
		return errNotReady(id, agent.status)
	}
	select {
	case agent.stdin <- append(raw, '\n'):
		return nil
	default:
		return fmt.Errorf("stdin queue full")
	}
}

// Transport-mappable sentinel errors: handlers test with errors.Is, never
// by matching message text.
var (
	// ErrUnknownAgent: no agent with the given id is registered.
	ErrUnknownAgent = errors.New("unknown agent")
	// ErrSpawnDepthExceeded: the spawn would exceed SPAWN_MAX_DEPTH (issue #216).
	ErrSpawnDepthExceeded = errors.New("spawn rejected: max depth exceeded")
	// ErrSpawnChildrenExceeded: the parent is at SPAWN_MAX_CHILDREN (issue #216).
	ErrSpawnChildrenExceeded = errors.New("spawn rejected: max children exceeded")
	// ErrTurnInFlight: a turn is already in flight; the prompt policy rejected it.
	ErrTurnInFlight = errors.New("has a turn in flight; abort or wait for settle")
	// ErrNotReady: the agent's FSM status does not permit the operation
	// (prompt on a non-ready agent, abort or steer without a turn in
	// flight).
	ErrNotReady = errors.New("agent not in a state permitting the operation")
)

func errUnknownAgent(id string) error { return fmt.Errorf("%w %q", ErrUnknownAgent, id) }
func errTurnInFlight(id string) error { return fmt.Errorf("agent %s %w", id, ErrTurnInFlight) }
func errNotReady(id string, s AgentStatus) error {
	return fmt.Errorf("agent %s %w (status %s)", id, ErrNotReady, s)
}

// startPi opens the exec stream and spawns the read/write/phase pumps.
func (m *AgentManager) startPi(agent *Agent, model string) error {
	// Agent lifetime == exec stream lifetime: one context per agent.
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := m.gw.StartPi(ctx, agent.sandboxID, model)
	if err != nil {
		cancel()
		return err
	}
	agent.stdin = make(chan []byte, 16)
	agent.cancel = cancel
	go stdinPump(stream, agent.stdin)
	go m.stdoutPump(agent, stream, cancel)
	go m.phasePoll(agent, ctx)
	return nil
}

// stdinPump forwards queued stdin bytes onto the exec stream.
func stdinPump(stream ExecStream, ch chan []byte) {
	for {
		b, ok := <-ch
		if !ok {
			return
		}
		if err := stream.Send(&v1.ExecSandboxInput{Payload: &v1.ExecSandboxInput_Stdin{Stdin: b}}); err != nil {
			log.Printf("[stdin] send failed: %v", err)
			return
		}
	}
}

// stdoutPump reads exec events: stdout JSONL pi records pass through verbatim
// into the hub; exit and stream break mark the agent failed.
func (m *AgentManager) stdoutPump(agent *Agent, stream ExecStream, cancel context.CancelFunc) {
	for {
		ev, err := stream.Recv()
		if err != nil {
			cancel()
			log.Printf("[agent %s] exec stream ended: %v", agent.ID, err)
			m.fail(agent, "exec stream ended: "+err.Error())
			return
		}
		switch p := ev.Payload.(type) {
		case *v1.ExecSandboxEvent_Stdout:
			for _, line := range splitLines(p.Stdout.Data) {
				if len(bytes.TrimSpace(line)) == 0 {
					continue
				}
				// pi occasionally emits non-JSON stdout lines (node warnings,
				// control sequences). Publish them wrapped — the event buffer
				// must stay JSON-safe or pull batches fail to encode wholesale
				// (found by the #179 live smoke: one bad line emptied every
				// read_events response).
				rec := bytes.TrimSpace(line)
				if !json.Valid(rec) {
					rec = mustJSON(map[string]string{"type": "raw", "data": string(rec)})
				}
				agent.hub.Publish(json.RawMessage(rec))
				m.onPiRecord(agent, json.RawMessage(rec))
			}
		case *v1.ExecSandboxEvent_Stderr:
			text := string(p.Stderr.Data)
			log.Printf("[agent %s][pi stderr] %s", agent.ID, text)
			agent.hub.Publish(json.RawMessage(mustJSON(map[string]string{"type": "stderr", "data": text})))
		case *v1.ExecSandboxEvent_Exit:
			agent.hub.Publish(json.RawMessage(mustJSON(map[string]interface{}{"type": "exec_exit", "exit_code": p.Exit.ExitCode})))
			log.Printf("[agent %s] pi exited with %d", agent.ID, p.Exit.ExitCode)
			if p.Exit.ExitCode != 0 {
				cancel()
				m.fail(agent, fmt.Sprintf("pi exited with code %d", p.Exit.ExitCode))
				return
			}
		}
	}
}

// onPiRecord watches verbatim pi records for the turn boundary: the
// agent_settled record ends a turn, flipping working → ready.
func (m *AgentManager) onPiRecord(agent *Agent, raw json.RawMessage) {
	var rec struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return
	}
	// Silence anchors for the monitor's telemetry (issue #232): last record
	// time + type, updated on every record while a timer is armed.
	m.monitor.NoteRecord(agent.ID, rec.Type)
	if rec.Type != piSettledEventType {
		return
	}
	agent.mu.Lock()
	if agent.status != StatusWorking {
		agent.mu.Unlock()
		return
	}
	agent.status = StatusReady
	agent.mu.Unlock()
	// Terminal assessment (issue #232, spec #230 Q10): disarm the timer and
	// invalidate in-flight ticks, then run the terminal assessment
	// synchronously (hard-bounded 10s) so the agent.assessed event appends
	// BEFORE agent.settled — every settle wake carries the terminal verdict.
	// Failure or budget exhaustion: settle proceeds without a verdict.
	agent.mgr.monitor.SettleAssess(agent.ID)
	// running → idle in the session FSM (issue #196).
	if err := agent.fsm.Transition(SessionIdle, "agent.settled", nil); err != nil {
		log.Printf("[agent %s] record settled failed: %v", agent.ID, err)
	}
	log.Printf("[agent %s] turn settled", agent.ID)
}

// phasePoll watches the sandbox phase for the agent's lifetime; ERROR flips
// the agent to failed even mid-turn.
func (m *AgentManager) phasePoll(agent *Agent, ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			phase, err := m.gw.GetSandboxPhase(ctx, agent.Sandbox)
			if err != nil {
				continue // transient poll errors are not agent failures
			}
			if phase == v1.SandboxPhase_SANDBOX_PHASE_ERROR {
				m.fail(agent, "sandbox phase ERROR")
				return
			}
		}
	}
}

// fail marks the agent failed (idempotent) and emits the agent.failed
// lifecycle event.
func (m *AgentManager) fail(agent *Agent, reason string) {
	agent.mu.Lock()
	if agent.status == StatusFailed || agent.status == StatusTerminated {
		agent.mu.Unlock()
		return
	}
	agent.status = StatusFailed
	// The session vocabulary has no failed state (issue #196: failures are
	// an agent-liveness concern; Epic 3 owns paused/reviving recovery), so
	// this lifecycle event is for the agent dimension and is ignored by the
	// session replay projection. Published INSIDE the critical section: any
	// observer that sees the failed status must observe the event too (the
	// status/event gap was a wait_agent race, issue #251).
	agent.hub.PublishLifecycle("agent.failed", map[string]interface{}{
		"agent_id": agent.ID,
		"status":   StatusFailed,
		"reason":   reason,
	})
	agent.mu.Unlock()
	// Liveness failure disarms the monitor timer (issue #232): a failed agent
	// is not running, no SLM spend accrues.
	m.monitor.Disarm(agent.ID)
	log.Printf("[agent %s] failed: %s", agent.ID, reason)
}

// splitLines splits on LF (strict JSONL framing), tolerating CRLF.
func splitLines(data []byte) [][]byte {
	return bytes.Split(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), []byte("\n"))
}
