package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	v1 "github.com/dzungtr/kape/controller/gen"
)

// ExecStream is the controller-observed subset of a bidirectional exec
// stream: writes reach the sandbox stdin, reads drain its stdout/stderr/exit
// events. The real Gateway's gRPC client stream and the test FakeStream both
// satisfy it.
type ExecStream interface {
	Send(*v1.ExecSandboxInput) error
	Recv() (*v1.ExecSandboxEvent, error)
}

// GatewayClient is the subset of the OpenShell gateway the AgentManager needs.
// The real Gateway implements it over mTLS gRPC; tests use gateway.FakeGateway
// so the status FSM and prompt policy run without a cluster.
type GatewayClient interface {
	// CreateSandbox creates a sandbox running the given image with the named
	// provider attached, the given resources, stamped with the given
	// ownership labels. Returns the canonical name and gateway UUID.
	CreateSandbox(ctx context.Context, name, image, provider string, resources Resources, labels map[string]string, piSessionID string) (sandboxName, sandboxID string, err error)
	// GetSandboxPhase polls the sandbox phase (used to detect READY and ERROR).
	GetSandboxPhase(ctx context.Context, name string) (v1.SandboxPhase, error)
	// ListSandboxes returns the sandboxes matching the label selector
	// ("k1=v1,k2=v2" format, per the gateway ListSandboxes API). The registry
	// joins these with in-memory live state; controllers filter the result
	// client-side too, so an empty selector result is never trusted as
	// authoritative for foreign-sandbox exclusion.
	ListSandboxes(ctx context.Context, labelSelector string) ([]SandboxInfo, error)
	// DeleteSandbox deletes the sandbox.
	DeleteSandbox(ctx context.Context, name string) error
	// StartPi opens the interactive exec stream running the pi RPC wrapper for
	// the canonical sandbox name. model ("" = leave the image's default model)
	// is patched into models.json and exported as PI_MODEL.
	StartPi(ctx context.Context, sandboxName, model string) (ExecStream, error)
	// ApplyModelGatewayPolicy merges the model-gateway egress rule for the sandbox.
	ApplyModelGatewayPolicy(ctx context.Context, sandboxName string) error
	// ApplyControllerEgressDeny merges the structural sandbox→controller
	// egress deny rule for the sandbox (issue #214, ADR-0083 D2/D7).
	ApplyControllerEgressDeny(ctx context.Context, sandboxName, host string, port int) error
	// ListProviders returns the provider names configured on the gateway
	// (credential-storage profiles). Powers list_providers (issue #220):
	// callers discover valid providers instead of guessing, and the
	// deployment's fixed provider comes from config, not the wire.
	ListProviders(ctx context.Context) ([]string, error)
}

// SandboxInfo is the registry's view of one gateway sandbox: the fields
// needed to join gateway state with the in-memory agent map.
type SandboxInfo struct {
	Name   string
	ID     string
	Phase  v1.SandboxPhase
	Labels map[string]string
}

// defaultWorkspace is the workspace every sandbox RPC is scoped to. OpenShell
// v0.1.0 requires a workspace_scope on every sandbox RPC (v0.0.116 implied it)
// and names the gateway's default workspace explicitly as "default" — an
// omitted selector is invalid, never a fallback.
const defaultWorkspace = "default"

// workspaceSelector builds the WorkspaceSelector a sandbox RPC must carry.
// Empty workspace selects the gateway's default workspace.
func workspaceSelector(workspace string) *v1.WorkspaceSelector {
	if workspace == "" {
		workspace = defaultWorkspace
	}
	return &v1.WorkspaceSelector{
		Selection: &v1.WorkspaceSelector_Workspace{Workspace: workspace},
	}
}

// Gateway is the real GatewayClient backed by the OpenShell gRPC surface.
type Gateway struct {
	client             v1.OpenShellClient
	modelGatewayURL    string    // base URL patched into the pi wrapper's models.json
	workspace          string    // workspace_scope carried by every sandbox RPC (v0.1.0); empty means defaultWorkspace
	daemonToken        string    // controller→daemon bearer placeholder (ADR-0083 D5); injected into the sandbox env so the in-sandbox daemon accepts the controller's pulls
	daemonCommand      []string  // canonical main process argv threaded as SandboxSpec.command (issue #247, spec #245 E2); nil keeps the gateway's default main process
	daemonPort         string    // in-sandbox daemon port the resolver dials (D7, default 9090); the daemon is told to bind it on all interfaces
	credentialBindings []Binding // env-fed credential binding table (KAPE_CREDENTIAL_BINDINGS, issue #264/H1); empty keeps the policy merge byte-identical
}

func NewGateway(conn *grpc.ClientConn, modelGatewayURL string) *Gateway {
	return &Gateway{client: v1.NewOpenShellClient(conn), modelGatewayURL: modelGatewayURL, workspace: defaultWorkspace}
}

// WithWorkspace scopes every sandbox RPC to the named workspace (v0.1.0
// workspace_scope). Empty keeps the gateway's default workspace.
func (g *Gateway) WithWorkspace(workspace string) *Gateway {
	g.workspace = workspace
	return g
}

// scope is the workspace_scope every sandbox RPC carries.
func (g *Gateway) scope() *v1.WorkspaceSelector { return workspaceSelector(g.workspace) }

// WithDaemonToken sets the D5 placeholder credential the daemon inside every
// newly created sandbox receives as KAPE_DAEMON_TOKEN template environment
// (issue #217 smoke requirement: the daemon's static-bearer check refuses an
// empty configured token, and the gRPC surface has no other env channel).
// Empty token (exec break-glass) leaves sandbox creation byte-identical.
func (g *Gateway) WithDaemonToken(token string) *Gateway {
	g.daemonToken = token
	return g
}

// WithCredentialBindings installs the env-fed binding table (#264, ADR-0088)
// the policy seam merges into every sandbox. Nil/empty keeps the merged policy
// byte-identical to the model-gateway-only posture.
func (g *Gateway) WithCredentialBindings(bindings []Binding) *Gateway {
	g.credentialBindings = bindings
	return g
}

// WithDaemonCommand sets the canonical main process argv passed as
// SandboxSpec.command on every created sandbox (issue #247, spec #245 E2):
// the OpenShell supervisor launches it directly, no shell parsing, so daemon
// lifetime == sandbox lifetime. Nil/empty keeps the request without a
// command (gateway default main process — the #217 profile-hook fallback
// posture).
// WithDaemonPort sets the fixed in-sandbox daemon port the controller dials
// (KAPE_DAEMON_PORT, D7). It is threaded into the daemon environment as the
// bind address, so the port the controller dials and the port the daemon
// listens on can never drift apart.
func (g *Gateway) WithDaemonPort(port string) *Gateway {
	g.daemonPort = port
	return g
}

func (g *Gateway) WithDaemonCommand(argv []string) *Gateway {
	g.daemonCommand = argv
	return g
}

// CreateSandbox creates a sandbox running the pi image with the named
// provider attached, the requested resources, stamped with the ownership
// labels (managed-by, agent-id) that back the label registry. piSessionID is
// the session-pinned pi --session-id (spec #245 E3, issue #249) threaded into
// the daemon environment so the sandbox's pi resumes the session's tree.
// Returns name and id.
func (g *Gateway) CreateSandbox(ctx context.Context, name, image, provider string, resources Resources, labels map[string]string, piSessionID string) (string, string, error) {
	req := g.createSandboxRequest(name, image, provider, resources, labels, g.daemonEnv(labels, piSessionID), g.daemonCommand)
	resp, err := g.client.CreateSandbox(ctx, req)
	if err != nil {
		return "", "", fmt.Errorf("CreateSandbox: %w", err)
	}
	return resp.Sandbox.Metadata.Name, resp.Sandbox.Metadata.Id, nil
}

// daemonEnv is the pull-transport environment (ADR-0083 D5/D7): the daemon's
// placeholder bearer check needs the token the controller will present on
// every pull/command; the session-pinned pi session id (spec #245 E3, #249)
// rides the same template environment. No command, no token, and no pinned id
// (exec break-glass) leaves sandbox creation byte-identical.
func (g *Gateway) daemonEnv(labels map[string]string, piSessionID string) map[string]string {
	env := sandboxEnv(g.daemonToken, labels[LabelAgentID], piSessionID)
	if g.daemonCommand != nil {
		// Main-process daemon (issue #247): no profile hook runs, so the PVC
		// spool path (#217 finding: spool lives under /sandbox) must be
		// injected here — the daemon's compiled default (/var/lib/kape-daemon)
		// is not writable by the sandbox uid (verified live on microk8s).
		if env == nil {
			env = map[string]string{}
		}
		env["KAPE_SPOOL_PATH"] = "/sandbox/kape-daemon-spool.jsonl"
		// The daemon's compiled default is 127.0.0.1:<port>, but the controller
		// dials the sandbox POD IP (:9090) through the CR resolver, so a
		// loopback bind is unreachable and every prompt dies as
		// "daemon rejected prompt: ... context deadline exceeded". Bind the
		// configured port on all interfaces instead; the sandbox's egress
		// policy still governs what the daemon can reach.
		port := g.daemonPort
		if port == "" {
			port = "9090"
		}
		env["KAPE_DAEMON_ADDR"] = "0.0.0.0:" + port
	}
	return env
}

// createSandboxRequest builds the CreateSandboxRequest payload; split out of
// CreateSandbox so the E2 wire contract (command threading, field 12) is
// unit-testable without a gRPC server.
func (g *Gateway) createSandboxRequest(name, image, provider string, resources Resources, labels map[string]string, env map[string]string, command []string) *v1.CreateSandboxRequest {
	req := &v1.CreateSandboxRequest{
		WorkspaceScope: g.scope(),
		Name:           name,
		Labels:         labels,
		Spec: &v1.SandboxSpec{
			Providers: []string{provider},
			Template: &v1.SandboxTemplate{
				Image: image,
				Resources: &structpb.Struct{
					Fields: map[string]*structpb.Value{
						"cpu":    structpb.NewStringValue(resources.CPU),
						"memory": structpb.NewStringValue(resources.Memory),
					},
				},
			},
		},
	}
	// Spec #245 E2: canonical main process — the sandbox supervisor launches
	// this argv directly (no shell parsing), making daemon lifetime equal the
	// sandbox lifetime.
	if len(command) > 0 {
		req.Spec.Command = command
	}
	if env != nil {
		req.Spec.Template.Environment = env
	}
	return req
}

// sandboxEnv builds the sandbox template environment for a new sandbox: the
// daemon token + agent id (pull transport credential, ADR-0083 D5/D7) and the
// session-pinned pi session id (spec #245 E3, issue #249). Empty values stay
// out of the map; an empty result keeps creation byte-identical.
func sandboxEnv(token, agentID, piSessionID string) map[string]string {
	env := map[string]string{}
	if token != "" {
		env["KAPE_DAEMON_TOKEN"] = token
		env["KAPE_AGENT_ID"] = agentID
	}
	if piSessionID != "" {
		env["KAPE_PI_SESSION_ID"] = piSessionID
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func (g *Gateway) ListProviders(ctx context.Context) ([]string, error) {
	resp, err := g.client.ListProviders(ctx, &v1.ListProvidersRequest{WorkspaceScope: g.scope()})
	if err != nil {
		return nil, fmt.Errorf("ListProviders: %w", err)
	}
	names := make([]string, 0, len(resp.Providers))
	for _, p := range resp.Providers {
		names = append(names, p.Metadata.Name)
	}
	return names, nil
}

// GetSandboxPhase polls the sandbox phase.
func (g *Gateway) GetSandboxPhase(ctx context.Context, name string) (v1.SandboxPhase, error) {
	resp, err := g.client.GetSandbox(ctx, &v1.GetSandboxRequest{WorkspaceScope: g.scope(), Name: name})
	if err != nil {
		return v1.SandboxPhase_SANDBOX_PHASE_UNSPECIFIED, err
	}
	return resp.Sandbox.Status.Phase, nil
}

// ListSandboxes pages the gateway ListSandboxes API with the given label
// selector and flattens it into SandboxInfo values. v0.1.0 pages by opaque
// token (page_size/page_token, next_page_token) instead of limit/offset.
func (g *Gateway) ListSandboxes(ctx context.Context, labelSelector string) ([]SandboxInfo, error) {
	var out []SandboxInfo
	const pageSize = int32(100)
	pageToken := ""
	for {
		resp, err := g.client.ListSandboxes(ctx, &v1.ListSandboxesRequest{
			WorkspaceScope: g.scope(),
			PageSize:       pageSize,
			PageToken:      pageToken,
			LabelSelector:  labelSelector,
		})
		if err != nil {
			return nil, fmt.Errorf("ListSandboxes: %w", err)
		}
		for _, sb := range resp.GetSandboxes() {
			md := sb.GetMetadata()
			labels := md.GetLabels()
			if labels == nil {
				labels = map[string]string{}
			}
			out = append(out, SandboxInfo{Name: md.GetName(), ID: md.GetId(), Phase: sb.GetStatus().GetPhase(), Labels: labels})
		}
		pageToken = resp.GetNextPageToken()
		if pageToken == "" {
			return out, nil
		}
	}
}

// WaitReady polls until the sandbox reaches READY (first image pull may take ~1min).
func (g *Gateway) WaitReady(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		phase, err := g.GetSandboxPhase(ctx, name)
		if err != nil {
			return fmt.Errorf("GetSandbox(%s): %w", name, err)
		}
		switch phase {
		case v1.SandboxPhase_SANDBOX_PHASE_READY:
			return nil
		case v1.SandboxPhase_SANDBOX_PHASE_ERROR:
			return fmt.Errorf("sandbox %s reached terminal phase %s", name, phase)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for sandbox %s to be READY", name)
		}
		time.Sleep(2 * time.Second)
	}
}

// DeleteSandbox deletes the sandbox.
func (g *Gateway) DeleteSandbox(ctx context.Context, name string) error {
	_, err := g.client.DeleteSandbox(ctx, &v1.DeleteSandboxRequest{WorkspaceScope: g.scope(), Name: name})
	if err != nil {
		return fmt.Errorf("DeleteSandbox(%s): %w", name, err)
	}
	return nil
}

// ProbeModelGateway runs a one-shot exec inside the sandbox to probe the
// configured model gateway and logs the result. Used to validate /models
// reachability and auth before pi starts. The probe targets the same endpoint
// the pi wrapper is about to use — a probe of any other host would report on
// something the sandbox is not configured to reach.
func (g *Gateway) ProbeModelGateway(ctx context.Context, sandbox string) {
	modelsURL := strings.TrimSuffix(g.modelGatewayURL, "/") + "/models"
	quoted := fmt.Sprintf("%q", modelsURL)
	cmd := []string{"bash", "-lc",
		"curl -s -m 8 -o /dev/null -w 'HTTP %{http_code}\\n' " + quoted +
			" && echo '--- body ---' " +
			"&& curl -s -m 8 " + quoted + " | head -c 2000 || echo 'curl missing/failed'"}
	req := &v1.ExecSandboxRequest{WorkspaceScope: g.scope(), Sandbox: sandbox, Command: cmd}
	stream, err := g.client.ExecSandbox(ctx, req)
	if err != nil {
		log.Printf("[probe] exec failed: %v", err)
		return
	}
	var out string
	for {
		ev, err := stream.Recv()
		if err != nil {
			log.Printf("[probe] stream error: %v", err)
			break
		}
		switch p := ev.Payload.(type) {
		case *v1.ExecSandboxEvent_Stdout:
			out += string(p.Stdout.Data)
		case *v1.ExecSandboxEvent_Stderr:
			out += string(p.Stderr.Data)
		}
	}
	log.Printf("[probe] model-gateway GET %s -> %s", modelsURL, out)
}

// StartPi opens an interactive exec stream running the pi RPC wrapper and
// returns the stream for later stdin writes.
func (g *Gateway) StartPi(ctx context.Context, sandboxName, model string) (ExecStream, error) {
	req := g.interactiveExecRequest(sandboxName, model)
	stream, err := g.client.ExecSandboxInteractive(ctx)
	if err != nil {
		return nil, fmt.Errorf("ExecSandboxInteractive open: %w", err)
	}
	if err := stream.Send(&v1.ExecSandboxInput{Payload: &v1.ExecSandboxInput_Start{Start: req}}); err != nil {
		return nil, fmt.Errorf("ExecSandboxInteractive start send: %w", err)
	}
	return stream, nil
}

// interactiveExecRequest builds the first frame of the interactive exec
// stream. v0.1.0 addresses the target by canonical sandbox name (field 1
// `sandbox`) and requires workspace_scope; the v0.0.116 pin sent a sandbox
// UUID in that slot, which the v0.1.0 gateway resolves as a name (not found).
func (g *Gateway) interactiveExecRequest(sandboxName, model string) *v1.ExecSandboxRequest {
	// NOTE: provider-injected env is withheld by the gateway for unbound static
	// credentials (spike finding); the model gateway is unauthenticated, so we
	// inject the key via the exec environment directly.
	env := map[string]string{"OPENROUTER_API_KEY": "dummy"}
	if model != "" {
		env["PI_MODEL"] = model
		env["PI_PROVIDER"] = "openrouter"
	}
	return &v1.ExecSandboxRequest{
		WorkspaceScope: g.scope(),
		Sandbox:        sandboxName,
		Command:        piWrapperCommand(g.modelGatewayURL, model),
		Workdir:        "/sandbox",
		Environment:    env,
	}
}

// piWrapperCommand patches the pi models.json: baseUrl to the configured
// model-gateway URL (pi env-interpolates apiKey/headers but NOT baseUrl) and,
// when a model override is requested, ensures that model id exists under
// providers.openrouter.models. It then execs pi in RPC mode. The model id is
// passed as argv to the python heredoc — never interpolated into the shell
// text itself.
func piWrapperCommand(modelGatewayURL, model string) []string {
	script := fmt.Sprintf(`set -e
cd /sandbox
MODELS="${PI_CODING_AGENT_DIR:-/sandbox/.pi/agent}/models.json"
python3 - "$MODELS" %q <<'PYEOF'
import json, sys
p, model = sys.argv[1], sys.argv[2]
with open(p) as f: m = json.load(f)
m["providers"]["openrouter"]["baseUrl"] = %q
if model:
    models = m["providers"]["openrouter"].setdefault("models", [])
    if not any(e.get("id") == model for e in models):
        models.append({"id": model})
with open(p, "w") as f: json.dump(m, f, indent=2)
print("patched baseUrl ->", m["providers"]["openrouter"]["baseUrl"], "model ->", model or "(image default)")
PYEOF
exec pi --mode rpc --no-session
`, model, modelGatewayURL)
	return []string{"bash", "-lc", script}
}

// newTlsConfig builds a TLS config trusting ca.crt and presenting the client cert.
func newTlsConfig(caPEM, certPEM, keyPEM []byte) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse ca.crt")
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse client cert/key: %w", err)
	}
	return &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}
