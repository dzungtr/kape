package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	v1 "github.com/dzungtr/kape/controller/gen"
)

// Label-registry constants: the ownership tags stamped on every sandbox this
// controller creates. The gateway's ListSandboxes API filters on them, so the
// registry needs no database: list = gateway sandboxes carrying managed-by,
// joined with in-memory live state. Foreign sandboxes (created by hand via
// the openshell CLI or another controller) never carry our managed-by value
// and are therefore never listed.
const (
	// LabelManagedBy marks which controller instance owns the sandbox.
	LabelManagedBy = "managed-by"
	// LabelAgentID carries the controller-assigned agent id, so get_agent can
	// resolve a sandbox back to an agent after a controller restart.
	LabelAgentID = "agent-id"
	// LabelParentAgentID carries the spawning parent's agent id (issue #216,
	// ADR-0083 D8): the durable linkage behind the children cap, the agent
	// view's parent field, and post-restart cascade termination.
	LabelParentAgentID = "parent-agent-id"
	// ManagedByValue is this controller's stable managed-by value. Recorded
	// in spec #172 Handoffs; changing it orphans existing sandboxes.
	ManagedByValue = "kape-controller"
)

// View sources: how the registry derived the view. "live" means the agent
// is in memory and this process owns its exec stream; "replayed" means it
// was rebuilt from the durable log at startup (issue #197: post-restart
// views are first-class, full state, no exec stream); "cr" means it came
// from gateway sandbox state only (legacy logs without the provisioning
// payload).
const (
	SourceLive     = "live"
	SourceReplayed = "replayed"
	SourceCR       = "cr"
)

// AgentView is the JSON shape for list_agents and get_agent: the joined
// registry record. Status is the FSM status for live agents and the
// CR-phase-derived status for post-restart agents.
type AgentView struct {
	ID      string      `json:"id"`
	Sandbox string      `json:"sandbox"`
	Status  AgentStatus `json:"status"`
	// Degraded is the transient pull-liveness flag (issue #213, D6): true
	// while the pull loop has hit degradedFailureThreshold consecutive pull
	// failures with no successful pull since. Never durable — a replayed or
	// CR-derived agent is never degraded.
	Degraded bool `json:"degraded,omitempty"`
	// Session is the durable session lifecycle FSM state (issue #196):
	// session = durable unit, agent = live instance. Derived from the FSM
	// for live agents; omitted for CR-derived (post-restart) views — their
	// replay is #197's scope.
	Session SessionState `json:"session,omitempty"`
	// Created is the controller-side creation time, known only for live
	// agents; post-restart it is omitted (the registry keeps no DB).
	Created *time.Time `json:"created,omitempty"`
	// Parent is the spawning parent's agent id (issue #216, D8); omitted for
	// root agents. Children lists the live children ids (terminated children
	// are detached); omitted when empty.
	Parent   string   `json:"parent,omitempty"`
	Children []string `json:"children,omitempty"`
	// Source records how this view was derived: "live" (in-memory agent,
	// exec state available) or "cr" (gateway sandbox labels/phase only).
	Source string `json:"source"`
	// Assessment is the minimal projected latest assessment of the current
	// turn (issue #232, spec #230 Q8): {verdict, summary, assessedAt, seq}.
	// Nil when the current turn has no assessment yet — a new turn's
	// agent.turn_started nulls the previous turn's terminal verdict.
	Assessment *AssessmentView `json:"assessment,omitempty"`
}

// agentView builds the view of a registered agent: "live" when this process
// owns the exec stream, "replayed" for a log-rehydrated agent (issue #197).
func (a *Agent) agentView() AgentView {
	var created *time.Time
	if !a.Created.IsZero() {
		c := a.Created
		created = &c
	}
	source := SourceLive
	if a.replayedOnly {
		source = SourceReplayed
	}
	a.mu.Lock()
	degraded := a.degraded
	a.mu.Unlock()
	view := AgentView{ID: a.ID, Sandbox: a.Sandbox, Status: a.Status(), Degraded: degraded, Session: a.fsm.State(), Created: created, Source: source}
	// Parent linkage (issue #216): shown only while the parent is still
	// registered (its deletion cascades, so a lingering parent id would
	// dangle). Children are the live registered agents with this parent.
	if p := a.mgr.Get(a.ParentID); p != nil && p.Status() != StatusTerminated {
		view.Parent = a.ParentID
	}
	children := a.mgr.liveChildIDs(a.ID)
	if len(children) > 0 {
		view.Children = children
	}
	// Latest-assessment projection (issue #232): read from the durable log —
	// full history stays in the log, only the minimal shape projects.
	if evs, err := a.hub.SubscribeSnapshot(); err == nil {
		view.Assessment = assessmentFromEvents(evs)
	}
	return view
}

// liveChildIDs returns the ids of registered, non-terminated children of the
// given parent.
func (m *AgentManager) liveChildIDs(parentID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for id, a := range m.agents {
		if a.ParentID == parentID && a.Status() != StatusTerminated {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// crView derives a post-restart view from gateway sandbox state: status comes
// from the sandbox phase alone — no exec state is available, so a READY
// sandbox looks ready even if pi is not running (documented v1 semantics:
// in-flight turns die at restart, the registry survives).
func crView(info SandboxInfo) AgentView {
	return AgentView{
		ID:      info.Labels[LabelAgentID],
		Sandbox: info.Name,
		Status:  statusFromPhase(info.Phase),
		Parent:  info.Labels[LabelParentAgentID], // durable parent linkage (issue #216)
		Source:  SourceCR,
	}
}

// assessmentFor replays the durable log for a CR-derived (post-restart)
// view and projects the latest current-turn assessment; nil when the log is
// unreadable or carries no assessment (issue #232).
func (m *AgentManager) assessmentFor(id string) *AssessmentView {
	evs, err := m.store.Replay(id, 0)
	if err != nil {
		return nil
	}
	return assessmentFromEvents(evs)
}

// statusFromPhase maps a gateway sandbox phase to the agent FSM status for
// CR-derived (post-restart) views. A STOPPED sandbox maps to creating under
// documented v1 semantics (no stopped status in the FSM); revisit if the FSM
// grows one. See PR #182 review notes.
func statusFromPhase(phase v1.SandboxPhase) AgentStatus {
	switch phase {
	case v1.SandboxPhase_SANDBOX_PHASE_READY:
		return StatusReady
	case v1.SandboxPhase_SANDBOX_PHASE_ERROR:
		return StatusFailed
	case v1.SandboxPhase_SANDBOX_PHASE_STOPPED:
		return StatusCreating
	default:
		return StatusCreating
	}
}

// owned checks that a sandbox is ours — client-side defense-in-depth on top
// of the gateway's label selector, so a selector-ignoring gateway backend can
// never leak foreign sandboxes into the registry.
func (m *AgentManager) owned(info SandboxInfo) bool {
	return info.Labels[LabelManagedBy] == m.managedBy && info.Labels[LabelAgentID] != ""
}

// List implements list_agents: gateway ListSandboxes filtered by the
// managed-by label, joined with in-memory state (live agents owned by this
// process, and replayed agents rebuilt from the durable log at startup —
// issue #197). CRs remain the enumeration/address source; agents not created
// by this controller are never listed.
func (m *AgentManager) List(ctx context.Context) ([]AgentView, error) {
	infos, err := m.gw.ListSandboxes(ctx, LabelManagedBy+"="+m.managedBy)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	live := make(map[string]*Agent, len(m.agents))
	for id, a := range m.agents {
		live[id] = a
	}
	m.mu.Unlock()

	views := make([]AgentView, 0, len(infos))
	for _, info := range infos {
		if !m.owned(info) {
			continue
		}
		id := info.Labels[LabelAgentID]
		if a, ok := live[id]; ok {
			view := a.agentView()
			if view.Sandbox == "" {
				// Legacy log without a recorded sandbox name: patch the view
				// from the CR — the CR stays the address source.
				view.Sandbox = info.Name
			}
			views = append(views, view)
			continue
		}
		cv := crView(info)
		cv.Assessment = m.assessmentFor(cv.ID)
		views = append(views, cv)
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Created != nil && views[j].Created != nil {
			return views[i].Created.Before(*views[j].Created)
		}
		return views[i].ID < views[j].ID
	})
	return views, nil
}

// ListProviders implements list_providers / GET /providers (issue #220,
// option B): proxies the gateway's provider list so callers discover valid
// provider names instead of guessing. The deployment's provider for new
// sandboxes is fixed by config — CreateProfile carries no provider field.
func (m *AgentManager) ListProviders(ctx context.Context) ([]string, error) {
	names, err := m.gw.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	if names == nil {
		names = []string{}
	}
	return names, nil
}

// GetView implements get_agent: the live in-memory view when the agent is in
// memory; otherwise (post-controller-restart) a CR-derived view from gateway
// sandbox state, or ErrUnknownAgent if no owned sandbox carries the agent-id.
func (m *AgentManager) GetView(ctx context.Context, id string) (AgentView, error) {
	if a := m.Get(id); a != nil {
		view := a.agentView()
		if view.Sandbox == "" {
			// Legacy log: patch the sandbox name from the CR (address source).
			view.Sandbox = m.crSandboxFor(ctx, id)
		}
		return view, nil
	}
	infos, err := m.gw.ListSandboxes(ctx, LabelManagedBy+"="+m.managedBy)
	if err != nil {
		return AgentView{}, err
	}
	for _, info := range infos {
		if m.owned(info) && info.Labels[LabelAgentID] == id {
			cv := crView(info)
			cv.Assessment = m.assessmentFor(id)
			return cv, nil
		}
	}
	return AgentView{}, errUnknownAgent(id)
}

// crSandboxFor resolves the sandbox name for an agent from the CR labels —
// the address-source join for replayed views whose log predates the recorded
// sandbox payload (issue #197).
func (m *AgentManager) crSandboxFor(ctx context.Context, id string) string {
	infos, err := m.gw.ListSandboxes(ctx, LabelManagedBy+"="+m.managedBy)
	if err != nil {
		return ""
	}
	for _, info := range infos {
		if m.owned(info) && info.Labels[LabelAgentID] == id {
			return info.Name
		}
	}
	return ""
}

// lifecycle event types whose order determines the replayed agent status.
// (The session projection in fsm.go owns the same vocabulary for the session
// dimension; these map onto the agent-liveness dimension instead.)
const (
	lifecycleProvisioned = "agent.provisioned"
	lifecycleStarted     = "agent.started"
	lifecycleTurnStarted = "agent.turn_started"
	lifecycleSettled     = "agent.settled"
	lifecycleFailed      = "agent.failed"
	lifecycleTerminated  = "agent.terminated"
)

// ReplayAgentStatus folds the agent-liveness projection over a log slice:
// the last status-carrying lifecycle event wins. Non-lifecycle records are
// ignored; the log is causally complete, so the result is deterministic.
func ReplayAgentStatus(evs []Event) AgentStatus {
	status := StatusCreating
	for i := range evs {
		switch evs[i].Type {
		case lifecycleProvisioned:
			status = StatusCreating
		case lifecycleStarted, lifecycleSettled:
			status = StatusReady
		case lifecycleTurnStarted:
			status = StatusWorking
		case lifecycleFailed:
			status = StatusFailed
		case lifecycleTerminated:
			status = StatusTerminated
		}
	}
	return status
}

// replayProvisionInfo extracts the provisioning payload from the log (sandbox
// name and creation time), empty/nil for legacy logs that predate it.
func replayProvisionInfo(evs []Event) (sandbox string, created *time.Time, parent string, resolved ResolvedProfile) {
	for i := range evs {
		if evs[i].Type != lifecycleProvisioned {
			continue
		}
		var fields struct {
			Sandbox  string `json:"sandbox"`
			Created  string `json:"created"`
			Parent   string `json:"parent"`
			Image    string `json:"image"`
			Provider string `json:"provider"`
			Model    string `json:"model"`
			CPU      string `json:"cpu"`
			Memory   string `json:"memory"`
		}
		if err := json.Unmarshal(evs[i].Data, &fields); err != nil {
			continue
		}
		sandbox = fields.Sandbox
		parent = fields.Parent
		if fields.Image != "" || fields.Provider != "" || fields.Model != "" || fields.CPU != "" || fields.Memory != "" {
			resolved = ResolvedProfile{
				Name:      fields.Sandbox,
				Image:     fields.Image,
				Provider:  fields.Provider,
				Model:     fields.Model,
				Resources: Resources{CPU: fields.CPU, Memory: fields.Memory},
			}
		}
		if fields.Created != "" {
			if t, err := time.Parse(time.RFC3339Nano, fields.Created); err == nil {
				created = &t
			}
		}
	}
	return sandbox, created, parent, resolved
}

// errRegistryReplayFailed wraps store/enumeration failures from startup
// replay; the controller logs and continues (CR-derived views stay
// available), rather than refusing to start.
var errRegistryReplayFailed = fmt.Errorf("registry replay failed")

// RebuildFromLog rehydrates the registry from the durable event log
// (issue #197): every agent with a log — except a terminated session, whose
// sandbox was deleted — is re-registered as a replayed-only agent with its
// hub opened (seq recovered, no gap) and its session FSM and agent status
// derived by replay. Post-restart agent views and event-surface reads
// (read_events/stream_events after cursors) become reachable immediately;
// the sandbox CRs remain the address source. A store read failure is
// returned wrapped; the caller may degrade to CR-derived views.
func (m *AgentManager) RebuildFromLog(ctx context.Context) error {
	ids, err := m.store.Agents()
	if err != nil {
		return fmt.Errorf("%w: %v", errRegistryReplayFailed, err)
	}
	for _, id := range ids {
		if m.Get(id) != nil {
			continue // live agent already registered; live state wins
		}
		hub, err := NewEventHub(m.store, id)
		if err != nil {
			return fmt.Errorf("%w: open hub for %s: %v", errRegistryReplayFailed, id, err)
		}
		evs, err := hub.SubscribeSnapshot()
		if err != nil {
			hub.Close()
			return fmt.Errorf("%w: replay %s: %v", errRegistryReplayFailed, id, err)
		}
		state := ReplaySession(evs)
		if state == SessionTerminated {
			// Deleted agent: no CR exists (the sandbox was deleted), so it
			// must not re-enter the registry — get_agent stays unknown, as
			// it was pre-restart.
			hub.Close()
			continue
		}
		sandbox, created, parent, resolved := replayProvisionInfo(evs)
		a := &Agent{
			ID:           id,
			Sandbox:      sandbox,
			ParentID:     parent, // D8: depth caps + cascade survive a restart
			resolved:     resolved,
			hub:          hub,
			fsm:          NewSessionFSM(hub, id, state),
			gw:           m.gw,
			mgr:          m,
			replayedOnly: true,
		}
		if created != nil {
			a.Created = *created
		}
		a.mu.Lock()
		a.status = ReplayAgentStatus(evs)
		a.mu.Unlock()
		m.mu.Lock()
		m.agents[id] = a
		m.mu.Unlock()
		log.Printf("[registry] replayed agent %s (session %s, status %s)", id, state, a.status)
		if m.cfg.Transport == TransportPull {
			// Pull transport (issues #212/#214): resume the per-agent puller
			// from the recovered origin watermark — the restart re-pull cannot
			// duplicate or skip a record. The daemon address resolves from the
			// Sandbox CR lazily on first pull (and re-resolves on every
			// reconnect); daemon-death detection (#213) is a later slice.
			ctx, cancel := context.WithCancel(context.Background())
			a.cancel = cancel
			a.pullTransport = true
			go m.runPuller(a, ctx)
		}
		// Restart re-arm (issue #232): a replayed agent whose projected state
		// is running gets its monitor timer re-armed from the replayed state —
		// timers are derived, never persisted (≤1 interval grace accepted).
		// Both projections must agree: the session FSM is blind to liveness
		// (fsm.go ignores agent.failed), so a liveness-failed agent replays
		// as running-session but dead (StatusFailed) — arming it would accrue
		// SLM spend and agent.assessed events on a dead agent.
		if state == SessionRunning && ReplayAgentStatus(evs) == StatusWorking {
			m.monitor.Arm(id, hub)
		}
	}
	return nil
}
