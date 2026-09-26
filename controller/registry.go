package main

import (
	"context"
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
	// ManagedByValue is this controller's stable managed-by value. Recorded
	// in spec #172 Handoffs; changing it orphans existing sandboxes.
	ManagedByValue = "kape-controller"
)

// View sources: how the registry derived the view. "live" means the agent is
// in memory (exec state available); "cr" means it came from gateway sandbox
// state only (post-restart; no exec state — documented v1 semantics).
const (
	SourceLive = "live"
	SourceCR   = "cr"
)

// AgentView is the JSON shape for list_agents and get_agent: the joined
// registry record. Status is the FSM status for live agents and the
// CR-phase-derived status for post-restart agents.
type AgentView struct {
	ID      string      `json:"id"`
	Sandbox string      `json:"sandbox"`
	Status  AgentStatus `json:"status"`
	// Created is the controller-side creation time, known only for live
	// agents; post-restart it is omitted (the registry keeps no DB).
	Created *time.Time `json:"created,omitempty"`
	// Source records how this view was derived: "live" (in-memory agent,
	// exec state available) or "cr" (gateway sandbox labels/phase only).
	Source string `json:"source"`
}

// agentView builds the live in-memory view of an agent.
func (a *Agent) agentView() AgentView {
	created := a.Created
	return AgentView{ID: a.ID, Sandbox: a.Sandbox, Status: a.Status(), Created: &created, Source: SourceLive}
}

// crView derives a post-restart view from gateway sandbox state: status comes
// from the sandbox phase alone — no exec state is available, so a READY
// sandbox looks ready even if pi is not running (documented v1 semantics:
// in-flight turns die at restart, the registry survives).
func crView(info SandboxInfo) AgentView {
	return AgentView{ID: info.Labels[LabelAgentID], Sandbox: info.Name, Status: statusFromPhase(info.Phase), Source: SourceCR}
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
// managed-by label, joined with in-memory live state. Agents not created by
// this controller are never listed.
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
			views = append(views, a.agentView())
			continue
		}
		views = append(views, crView(info))
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Created != nil && views[j].Created != nil {
			return views[i].Created.Before(*views[j].Created)
		}
		return views[i].ID < views[j].ID
	})
	return views, nil
}

// GetView implements get_agent: the live in-memory view when the agent is in
// memory; otherwise (post-controller-restart) a CR-derived view from gateway
// sandbox state, or ErrUnknownAgent if no owned sandbox carries the agent-id.
func (m *AgentManager) GetView(ctx context.Context, id string) (AgentView, error) {
	if a := m.Get(id); a != nil {
		return a.agentView(), nil
	}
	infos, err := m.gw.ListSandboxes(ctx, LabelManagedBy+"="+m.managedBy)
	if err != nil {
		return AgentView{}, err
	}
	for _, info := range infos {
		if m.owned(info) && info.Labels[LabelAgentID] == id {
			return crView(info), nil
		}
	}
	return AgentView{}, errUnknownAgent(id)
}
