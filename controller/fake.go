package main

import (
	"context"
	"fmt"
	"sync"

	v1 "github.com/dzungtr/kape/controller/gen"
)

// FakeGateway is an in-memory GatewayClient for unit tests of the AgentManager
// status FSM and prompt policy: no cluster, no gRPC. Tests drive sandbox phase
// transitions directly (SetPhase) and push exec events into the FakeStream
// returned by StartPi.
type FakeGateway struct {
	mu    sync.Mutex
	phase map[string]v1.SandboxPhase
	// initialPhase is the phase a newly created sandbox starts in; tests
	// default it to READY so Create's waitReady loop returns immediately.
	initialPhase v1.SandboxPhase
	deleted      []string
	created      []string
	// stream, if set, is returned by every StartPi call.
	stream *FakeStream
}

func NewFakeGateway() *FakeGateway {
	return &FakeGateway{
		phase:        map[string]v1.SandboxPhase{},
		initialPhase: v1.SandboxPhase_SANDBOX_PHASE_READY,
	}
}

func (f *FakeGateway) CreateSandbox(ctx context.Context, name, image, provider string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, name)
	f.phase[name] = f.initialPhase
	return name, "uuid-" + name, nil
}

func (f *FakeGateway) GetSandboxPhase(ctx context.Context, name string) (v1.SandboxPhase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	phase, ok := f.phase[name]
	if !ok {
		return v1.SandboxPhase_SANDBOX_PHASE_UNSPECIFIED, fmt.Errorf("unknown sandbox %q", name)
	}
	return phase, nil
}

// SetPhase moves a fake sandbox's phase; the AgentManager's phase poll reacts.
func (f *FakeGateway) SetPhase(name string, phase v1.SandboxPhase) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.phase[name] = phase
}

func (f *FakeGateway) DeleteSandbox(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, name)
	return nil
}

// SetStream installs the FakeStream handed out by StartPi.
func (f *FakeGateway) SetStream(s *FakeStream) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stream = s
}

func (f *FakeGateway) StartPi(ctx context.Context, sandboxID string) (ExecStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stream == nil {
		return nil, fmt.Errorf("no fake stream installed")
	}
	return f.stream, nil
}

func (f *FakeGateway) ApplyModelGatewayPolicy(ctx context.Context, sandboxName string) error {
	return nil
}

// DeletedSandboxes returns sandbox names passed to DeleteSandbox.
func (f *FakeGateway) DeletedSandboxes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.deleted))
	copy(out, f.deleted)
	return out
}

// FakeStream is a scriptable ExecStream: sends are recorded, and Recv yields
// events pushed via Deliver until the stream is closed (Fail/Close), which
// surfaces as an error (exec stream break / pi exit).
type FakeStream struct {
	mu     sync.Mutex
	sent   [][]byte
	events chan *v1.ExecSandboxEvent
	closed bool
	err    error
	done   chan struct{}
}

func NewFakeStream() *FakeStream {
	return &FakeStream{events: make(chan *v1.ExecSandboxEvent, 64), done: make(chan struct{})}
}

func (s *FakeStream) Send(in *v1.ExecSandboxInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if st, ok := in.Payload.(*v1.ExecSandboxInput_Stdin); ok {
		s.sent = append(s.sent, st.Stdin)
	}
	return nil
}

func (s *FakeStream) Recv() (*v1.ExecSandboxEvent, error) {
	select {
	case ev := <-s.events:
		return ev, nil
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return nil, s.err
	}
}

// Deliver pushes a stdout JSONL line into the stream (what the sandbox pi
// process would print). Callers may split on newlines themselves.
func (s *FakeStream) Deliver(stdout string) {
	s.events <- &v1.ExecSandboxEvent{Payload: &v1.ExecSandboxEvent_Stdout{Stdout: &v1.ExecSandboxStdout{Data: []byte(stdout)}}}
}

// Fail terminates the stream like an exec stream break.
func (s *FakeStream) Fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	close(s.done)
}

// Exit terminates the stream like the sandbox process exiting.
func (s *FakeStream) Exit(code int32) {
	s.events <- &v1.ExecSandboxEvent{Payload: &v1.ExecSandboxEvent_Exit{Exit: &v1.ExecSandboxExit{ExitCode: code}}}
	s.Fail(fmt.Errorf("process exited"))
}

// SentPayloads returns the stdin bytes written to the stream.
func (s *FakeStream) SentPayloads() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.sent))
	copy(out, s.sent)
	return out
}
