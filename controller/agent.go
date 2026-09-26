package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

	mu     sync.Mutex
	status AgentStatus

	sandboxID string // gateway sandbox UUID (used by exec RPCs)
	piSeq     int    // last pi RPC prompt id issued

	hub    *EventHub // buffered + fan-out of raw JSONL records from pi stdout
	stdin  chan []byte
	cancel context.CancelFunc
	gw     GatewayClient
	mgr    *AgentManager
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
}

func NewAgentManager(gw GatewayClient, cfg Config) *AgentManager {
	return &AgentManager{agents: map[string]*Agent{}, gw: gw, cfg: cfg, managedBy: ManagedByValue}
}

// Create provisions a sandbox for the (possibly partial) profile override
// set, waits for READY, applies the model-gateway egress policy, then opens
// the long-lived pi exec stream. Validation happens BEFORE any gateway call,
// so a rejected create never leaks a sandbox. On later (gateway) failure the
// sandbox is deleted and an error returned. On success the agent is
// StatusReady; a phase-poll goroutine watches for ERROR phases and a pi-pump
// goroutine watches the exec stream for the agent's lifetime.
func (m *AgentManager) Create(ctx context.Context, profile *CreateProfile) (*Agent, error) {
	resolved, err := m.cfg.ResolveProfile(profile)
	if err != nil {
		return nil, err
	}
	id := randID("agent")
	if resolved.Name == "" {
		resolved.Name = "sbx-" + id
	}
	name := resolved.Name
	agent := &Agent{ID: id, Sandbox: name, Created: time.Now(), hub: NewEventHub(), gw: m.gw, mgr: m}

	agent.mu.Lock()
	agent.status = StatusCreating
	agent.mu.Unlock()
	log.Printf("[agent %s] creating sandbox %s (image %s, provider %s, cpu %s, memory %s, model %q)",
		id, name, resolved.Image, resolved.Provider, resolved.Resources.CPU, resolved.Resources.Memory, resolved.Model)

	_, sbID, err := m.gw.CreateSandbox(ctx, name, resolved.Image, resolved.Provider, resolved.Resources, map[string]string{
		LabelManagedBy: m.managedBy, // ownership tag: the label registry's filter key
		LabelAgentID:   id,          // agent-id: resolves sandbox → agent after restart
	})
	if err != nil {
		return nil, err
	}
	agent.sandboxID = sbID
	if err := m.waitReady(ctx, agent); err != nil {
		_ = m.gw.DeleteSandbox(context.Background(), name)
		return nil, err
	}
	log.Printf("[agent %s] sandbox %s READY", id, name)
	agent.hub.PublishLifecycle("agent.provisioned", map[string]interface{}{
		"agent_id": agent.ID,
		"status":   StatusCreating,
	})

	if err := m.gw.ApplyModelGatewayPolicy(ctx, name); err != nil {
		_ = m.gw.DeleteSandbox(context.Background(), name)
		return nil, fmt.Errorf("apply network policy: %w", err)
	}

	if err := m.startPi(agent, resolved.Model); err != nil {
		_ = m.gw.DeleteSandbox(context.Background(), name)
		return nil, err
	}

	m.mu.Lock()
	m.agents[id] = agent
	m.mu.Unlock()

	agent.setStatusAndEmit(StatusReady, "agent.started")
	return agent, nil
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

// Delete aborts the exec stream, deletes the sandbox, removes the agent.
func (m *AgentManager) Delete(ctx context.Context, id string) error {
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
	close(agent.stdin)
	agent.mu.Unlock()
	agent.hub.PublishLifecycle("agent.terminated", map[string]interface{}{
		"agent_id": agent.ID,
		"status":   StatusTerminated,
	})
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

// sendPrompt atomically enforces the prompt policy and, if accepted, writes
// the pi RPC prompt command to the exec stdin ({"id","type":"prompt",
// "message"} — verified against local pi docs, docs/rpc-commands.md §prompt)
// and flips the agent to working.
func (a *Agent) sendPrompt(message string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch a.status {
	case StatusWorking:
		return errTurnInFlight(a.ID)
	case StatusReady:
		// accepted below
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
		a.status = StatusWorking
		log.Printf("[agent %s] prompt accepted: %q", a.ID, message)
		return nil
	default:
		return fmt.Errorf("stdin queue full")
	}
}

// Abort stops the in-flight turn by sending the pi RPC abort command
// ({"type":"abort"}). The exec stream stays open: pi settles the turn and
// subsequent records still reach the event stream, flipping the agent back
// to ready on the settled record.
func (m *AgentManager) Abort(id string) error {
	agent := m.Get(id)
	if agent == nil {
		return errUnknownAgent(id)
	}
	if agent.Status() != StatusWorking {
		return errNotReady(id, agent.Status()) // fast path
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
	// ErrTurnInFlight: a turn is already in flight; the prompt policy rejected it.
	ErrTurnInFlight = errors.New("has a turn in flight; abort or wait for settle")
	// ErrNotReady: the agent's FSM status does not permit the operation
	// (prompt on a non-ready agent, abort without a turn in flight).
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
				agent.hub.Publish(json.RawMessage(line))
				m.onPiRecord(agent, json.RawMessage(line))
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
	if err := json.Unmarshal(raw, &rec); err != nil || rec.Type != piSettledEventType {
		return
	}
	agent.mu.Lock()
	if agent.status == StatusWorking {
		agent.status = StatusReady
		agent.mu.Unlock()
		log.Printf("[agent %s] turn settled", agent.ID)
		return
	}
	agent.mu.Unlock()
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
	agent.mu.Unlock()
	log.Printf("[agent %s] failed: %s", agent.ID, reason)
	agent.hub.PublishLifecycle("agent.failed", map[string]interface{}{
		"agent_id": agent.ID,
		"status":   StatusFailed,
		"reason":   reason,
	})
}

// splitLines splits on LF (strict JSONL framing), tolerating CRLF.
func splitLines(data []byte) [][]byte {
	return bytes.Split(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), []byte("\n"))
}
