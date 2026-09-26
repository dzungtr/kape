package main

import (
	"context"
	"crypto/x509"
	"crypto/tls"
	"fmt"
	"log"
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
	// provider attached. Returns the canonical name and gateway UUID.
	CreateSandbox(ctx context.Context, name, image, provider string) (sandboxName, sandboxID string, err error)
	// GetSandboxPhase polls the sandbox phase (used to detect READY and ERROR).
	GetSandboxPhase(ctx context.Context, name string) (v1.SandboxPhase, error)
	// DeleteSandbox deletes the sandbox.
	DeleteSandbox(ctx context.Context, name string) error
	// StartPi opens the interactive exec stream running the pi RPC wrapper.
	StartPi(ctx context.Context, sandboxID string) (ExecStream, error)
	// ApplyModelGatewayPolicy merges the model-gateway egress rule for the sandbox.
	ApplyModelGatewayPolicy(ctx context.Context, sandboxName string) error
}

// Gateway is the real GatewayClient backed by the OpenShell gRPC surface.
type Gateway struct {
	client v1.OpenShellClient
}

func NewGateway(conn *grpc.ClientConn) *Gateway {
	return &Gateway{client: v1.NewOpenShellClient(conn)}
}

// CreateSandbox creates a sandbox running the pi image with the named
// provider attached and modest resources. Returns the canonical name and id.
func (g *Gateway) CreateSandbox(ctx context.Context, name, image, provider string) (string, string, error) {
	req := &v1.CreateSandboxRequest{
		Name: name,
		Spec: &v1.SandboxSpec{
			Providers: []string{provider},
			Template: &v1.SandboxTemplate{
				Image: image,
				Resources: &structpb.Struct{
					Fields: map[string]*structpb.Value{
						"cpu":    structpb.NewStringValue("1"),
						"memory": structpb.NewStringValue("2Gi"),
					},
				},
			},
		},
	}
	resp, err := g.client.CreateSandbox(ctx, req)
	if err != nil {
		return "", "", fmt.Errorf("CreateSandbox: %w", err)
	}
	return resp.Sandbox.Metadata.Name, resp.Sandbox.Metadata.Id, nil
}

// GetSandboxPhase polls the sandbox phase.
func (g *Gateway) GetSandboxPhase(ctx context.Context, name string) (v1.SandboxPhase, error) {
	resp, err := g.client.GetSandbox(ctx, &v1.GetSandboxRequest{Name: name})
	if err != nil {
		return v1.SandboxPhase_SANDBOX_PHASE_UNSPECIFIED, err
	}
	return resp.Sandbox.Status.Phase, nil
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
	_, err := g.client.DeleteSandbox(ctx, &v1.DeleteSandboxRequest{Name: name})
	if err != nil {
		return fmt.Errorf("DeleteSandbox(%s): %w", name, err)
	}
	return nil
}

// ProbeModelGateway runs a one-shot exec inside the sandbox to probe the
// in-cluster model gateway and logs the result. Used to validate /v1/models
// reachability and auth before pi starts.
func (g *Gateway) ProbeModelGateway(ctx context.Context, id string) {
	cmd := []string{"bash", "-lc",
		"curl -s -m 8 -o /dev/null -w 'HTTP %{http_code}\\n' http://model-gateway-http.aperture.svc.cluster.local/v1/models " +
			"&& echo '--- body ---' " +
			"&& curl -s -m 8 http://model-gateway-http.aperture.svc.cluster.local/v1/models | head -c 2000 || echo 'curl missing/failed'"}
	req := &v1.ExecSandboxRequest{SandboxId: id, Command: cmd}
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
	log.Printf("[probe] model-gateway GET /v1/models -> %s", out)
}

// StartPi opens an interactive exec stream running the pi RPC wrapper and
// returns the stream for later stdin writes.
func (g *Gateway) StartPi(ctx context.Context, id string) (ExecStream, error) {
	// NOTE: provider-injected env is withheld by the gateway for unbound static
// credentials (spike finding); the model gateway is unauthenticated, so we
// inject the key via the exec environment directly.
	req := &v1.ExecSandboxRequest{SandboxId: id, Command: piWrapperCommand(), Workdir: "/sandbox",
		Environment: map[string]string{"OPENROUTER_API_KEY": "dummy"}}
	stream, err := g.client.ExecSandboxInteractive(ctx)
	if err != nil {
		return nil, fmt.Errorf("ExecSandboxInteractive open: %w", err)
	}
	if err := stream.Send(&v1.ExecSandboxInput{Payload: &v1.ExecSandboxInput_Start{Start: req}}); err != nil {
		return nil, fmt.Errorf("ExecSandboxInteractive start send: %w", err)
	}
	return stream, nil
}

// piWrapperCommand patches the pi models.json baseUrl to the in-cluster model
// gateway (pi env-interpolates apiKey/headers but NOT baseUrl), then execs pi
// in RPC mode.
func piWrapperCommand() []string {
	script := `set -e
cd /sandbox
MODELS="${PI_CODING_AGENT_DIR:-/sandbox/.pi/agent}/models.json"
if command -v python3 >/dev/null 2>&1; then
  python3 - "$MODELS" <<'PYEOF'
import json, sys
p = sys.argv[1]
with open(p) as f: m = json.load(f)
m["providers"]["openrouter"]["baseUrl"] = "http://model-gateway-http.aperture.svc.cluster.local/v1"
with open(p, "w") as f: json.dump(m, f, indent=2)
print("patched baseUrl ->", m["providers"]["openrouter"]["baseUrl"])
PYEOF
else
  sed -i 's|"baseUrl": *"[^"]*"|"baseUrl": "http://model-gateway-http.aperture.svc.cluster.local/v1"|' "$MODELS"
  echo "patched baseUrl via sed"
fi
exec pi --mode rpc --no-session
`
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
