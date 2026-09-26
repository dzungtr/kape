package main

import (
	"context"
	"fmt"
	"strings"
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
	// labels holds the labels stamped on each sandbox, keyed by name. Real
	// gateways persist the CreateSandbox labels; the fake records them so
	// registry tests can assert the ownership tags landed.
	labels map[string]map[string]string
	// ids overrides a sandbox's gateway UUID when seeded (SeedSandbox); the
	// default is "uuid-<name>" as CreateSandbox returns.
	ids map[string]string
	// initialPhase is the phase a newly created sandbox starts in; tests
	// default it to READY so Create's waitReady loop returns immediately.
	initialPhase v1.SandboxPhase
	deleted      []string
	created      []string
	requests     []CreateRequest
	piModels     []string
	// stream, if set, is returned by every StartPi call.
	stream *FakeStream
}

func NewFakeGateway() *FakeGateway {
	return &FakeGateway{
		phase:        map[string]v1.SandboxPhase{},
		labels:       map[string]map[string]string{},
		ids:          map[string]string{},
		initialPhase: v1.SandboxPhase_SANDBOX_PHASE_READY,
	}
}

func (f *FakeGateway) CreateSandbox(ctx context.Context, name, image, provider string, resources Resources, labels map[string]string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, name)
	f.requests = append(f.requests, CreateRequest{Name: name, Image: image, Provider: provider, Resources: resources})
	f.phase[name] = f.initialPhase
	if labels == nil {
		labels = map[string]string{}
	}
	f.labels[name] = labels
	return name, "uuid-" + name, nil
}

// CreateRequest records one CreateSandbox call for assertions on overrides.
type CreateRequest struct {
	Name      string
	Image     string
	Provider  string
	Resources Resources
}

// CreatedRequests returns the recorded CreateSandbox requests.
func (f *FakeGateway) CreatedRequests() []CreateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]CreateRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// SeedSandbox installs a pre-existing sandbox with the given labels and
// phase — used to model foreign sandboxes and post-restart CR-derived state.
func (f *FakeGateway) SeedSandbox(name, id string, labels map[string]string, phase v1.SandboxPhase) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.phase[name] = phase
	f.labels[name] = labels
	f.ids[name] = id
}

// ListSandboxes returns the seeded/created sandboxes matching the
// "k1=v1,k2=v2" label selector, mirroring the gateway's ListSandboxes
// filtering contract.
func (f *FakeGateway) ListSandboxes(ctx context.Context, labelSelector string) ([]SandboxInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sel, err := parseLabelSelector(labelSelector)
	if err != nil {
		return nil, err
	}
	var out []SandboxInfo
	for name, labels := range f.labels {
		if !labelsMatch(labels, sel) {
			continue
		}
		id := f.ids[name]
		if id == "" {
			id = "uuid-" + name
		}
		out = append(out, SandboxInfo{Name: name, ID: id, Phase: f.phase[name], Labels: labels})
	}
	return out, nil
}

// parseLabelSelector parses the gateway's "k1=v1,k2=v2" label-selector format.
func parseLabelSelector(sel string) (map[string]string, error) {
	out := map[string]string{}
	if sel == "" {
		return out, nil
	}
	for _, part := range strings.Split(sel, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("invalid label selector %q", sel)
		}
		out[k] = v
	}
	return out, nil
}

func labelsMatch(labels, sel map[string]string) bool {
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// LabelsFor returns the labels stamped on a sandbox (test assertion helper).
func (f *FakeGateway) LabelsFor(name string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for k, v := range f.labels[name] {
		out[k] = v
	}
	return out
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

func (f *FakeGateway) StartPi(ctx context.Context, sandboxID, model string) (ExecStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.piModels = append(f.piModels, model)
	if f.stream == nil {
		return nil, fmt.Errorf("no fake stream installed")
	}
	return f.stream, nil
}

// PiModels returns the model argument of each StartPi call (in order).
func (f *FakeGateway) PiModels() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.piModels))
	copy(out, f.piModels)
	return out
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
