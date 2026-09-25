package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	v1 "github.com/dzungtr/kape/prototype/m1-controller/gen"
)

// Session owns one sandbox + one pi RPC process (the exec stream).
// This is the M1 property under test: the pi process and the exec gRPC stream
// are bound to this connection's lifetime.
type Session struct {
	ID      string    `json:"id"`
	Sandbox string    `json:"sandbox"`
	Created time.Time `json:"created"`

	sandboxID string // gateway sandbox UUID (used by exec RPCs)

	mu   sync.Mutex
	hub  *EventHub // buffered + fan-out of raw JSONL records from pi stdout
	piMu sync.Mutex
	piID int

	stdin  chan []byte        // pi RPC stdin queue (consumed by stdinPump)
	cancel context.CancelFunc // aborts the exec stream == session lifetime
	gw     *Gateway
	mgr    *SessionManager
}

// EventHub buffers all events and supports multiple SSE subscribers.
type EventHub struct {
	mu          sync.Mutex
	buffer      []json.RawMessage
	subscribers map[int]chan json.RawMessage
	nextSub     int
	closed      bool
}

func NewEventHub() *EventHub {
	return &EventHub{subscribers: map[int]chan json.RawMessage{}}
}

// Publish appends an event to the buffer and fans out to subscribers.
func (h *EventHub) Publish(raw json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.buffer = append(h.buffer, raw)
	for _, ch := range h.subscribers {
		select {
		case ch <- raw:
		default: // slow subscriber: drop rather than stall the pi stream
		}
	}
}

// Subscribe returns the buffered replay plus a live channel.
func (h *EventHub) Subscribe() ([]json.RawMessage, chan json.RawMessage, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	replay := make([]json.RawMessage, len(h.buffer))
	copy(replay, h.buffer)
	ch := make(chan json.RawMessage, 256)
	id := h.nextSub
	h.nextSub++
	h.subscribers[id] = ch
	return replay, ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subscribers, id)
	}
}

func (h *EventHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
}

// SessionManager tracks sessions in memory only.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	gw       *Gateway
	provider string
	image    string
}

func NewSessionManager(gw *Gateway, provider, image string) *SessionManager {
	return &SessionManager{sessions: map[string]*Session{}, gw: gw, provider: provider, image: image}
}

// Create provisions a sandbox, waits for READY, probes the model gateway,
// then opens the long-lived pi exec stream.
func (m *SessionManager) Create(ctx context.Context) (*Session, error) {
	id := randID("m1")
	name := "sbx-" + id
	sess := &Session{ID: id, Sandbox: name, Created: time.Now(), hub: NewEventHub(), gw: m.gw, mgr: m}

	log.Printf("[session %s] creating sandbox %s (image %s, provider %s)", id, name, m.image, m.provider)
	_, sbID, err := m.gw.CreateSandbox(ctx, name, m.image, m.provider)
	if err != nil {
		return nil, err
	}
	sess.sandboxID = sbID
	if err := m.gw.WaitReady(ctx, name, 10*time.Minute); err != nil {
		_ = m.gw.DeleteSandbox(context.Background(), name)
		return nil, err
	}
	log.Printf("[session %s] sandbox %s READY", id, name)

	m.gw.ProbeModelGateway(ctx, sbID)

	if err := m.gw.ApplyModelGatewayPolicy(ctx, name); err != nil {
		_ = m.gw.DeleteSandbox(context.Background(), name)
		return nil, fmt.Errorf("apply network policy: %w", err)
	}

	if err := m.startPi(sess); err != nil {
		_ = m.gw.DeleteSandbox(context.Background(), name)
		return nil, err
	}

	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()
	return sess, nil
}

// Get returns a session or nil.
func (m *SessionManager) Get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

// Delete aborts the exec, deletes the sandbox, removes the session.
func (m *SessionManager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	sess := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if sess == nil {
		return fmt.Errorf("unknown session %q", id)
	}
	sess.hub.Close()
	if err := m.gw.DeleteSandbox(ctx, sess.Sandbox); err != nil {
		return err
	}
	log.Printf("[session %s] sandbox %s deleted", id, sess.Sandbox)
	return nil
}

// startPi opens the exec stream and spawns the read/write pumps.
func (m *SessionManager) startPi(sess *Session) error {
	// Session lifetime == exec stream lifetime: one context per session.
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := m.gw.StartPi(ctx, sess.sandboxID)
	if err != nil {
		cancel()
		return err
	}
	stdinCh := make(chan []byte, 16)
	go stdinPump(stream, stdinCh)
	go stdoutPump(sess, stream, cancel)
	sess.stdin = stdinCh
	sess.cancel = cancel
	return nil
}

// stdinPump forwards queued stdin bytes onto the exec stream.
func stdinPump(stream v1.OpenShell_ExecSandboxInteractiveClient, ch chan []byte) {
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

// stdoutPump reads exec events and feeds stdout JSONL lines into the hub.
func stdoutPump(sess *Session, stream v1.OpenShell_ExecSandboxInteractiveClient, cancel context.CancelFunc) {
	for {
		ev, err := stream.Recv()
		if err != nil {
			cancel()
			log.Printf("[session %s] exec stream ended: %v", sess.ID, err)
			sess.mgr.mu.Lock()
			delete(sess.mgr.sessions, sess.ID)
			sess.mgr.mu.Unlock()
			sess.hub.Close()
			return
		}
		switch p := ev.Payload.(type) {
		case *v1.ExecSandboxEvent_Stdout:
			for _, line := range splitLines(p.Stdout.Data) {
				if len(bytes.TrimSpace(line)) == 0 {
					continue
				}
				sess.hub.Publish(json.RawMessage(line))
			}
		case *v1.ExecSandboxEvent_Stderr:
			text := string(p.Stderr.Data)
			log.Printf("[session %s][pi stderr] %s", sess.ID, text)
			wrapped, _ := json.Marshal(map[string]string{"type": "stderr", "data": text})
			sess.hub.Publish(wrapped)
		case *v1.ExecSandboxEvent_Exit:
			wrapped, _ := json.Marshal(map[string]interface{}{"type": "exec_exit", "exit_code": p.Exit.ExitCode})
			sess.hub.Publish(wrapped)
			log.Printf("[session %s] pi exited with %d", sess.ID, p.Exit.ExitCode)
		}
	}
}

// splitLines splits on LF (strict JSONL framing), tolerating CRLF.
func splitLines(data []byte) [][]byte {
	return bytes.Split(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), []byte("\n"))
}

// Prompt writes a pi RPC prompt command to the exec stdin.
func (s *Session) Prompt(message string) error {
	s.piMu.Lock()
	defer s.piMu.Unlock()
	s.piID++
	cmd := map[string]interface{}{"id": fmt.Sprintf("p%d", s.piID), "type": "prompt", "message": message}
	raw, _ := json.Marshal(cmd)
	select {
	case s.stdin <- append(raw, '\n'):
		return nil
	default:
		return fmt.Errorf("stdin queue full")
	}
}
