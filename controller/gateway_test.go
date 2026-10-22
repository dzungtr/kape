package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"google.golang.org/grpc"

	v1 "github.com/dzungtr/kape/controller/gen"
)

// createSandboxRequest builds the CreateSandboxRequest the gateway create
// path sends. Issue #247 (spec #245 E2): Spec.Command carries the daemon as
// the sandbox's canonical main process — no shell wrapping, no profile hook.

func TestCreateSandboxRequest_ThreadsDaemonCommand(t *testing.T) {
	req := (&Gateway{}).createSandboxRequest("sbx-test", "img:1", "prov",
		Resources{CPU: "1", Memory: "2Gi"},
		map[string]string{"managed-by": "kape-controller"},
		map[string]string{"KAPE_DAEMON_TOKEN": "t"},
		[]string{"kape-daemon"})
	if got := req.Spec.Command; len(got) != 1 || got[0] != "kape-daemon" {
		t.Fatalf("Spec.Command = %v, want [kape-daemon] (proto field 12, no shell wrapping)", got)
	}
	if req.Spec.Template.Image != "img:1" {
		t.Fatalf("image = %q", req.Spec.Template.Image)
	}
	if req.Spec.Template.Environment["KAPE_DAEMON_TOKEN"] != "t" {
		t.Fatal("daemon token env missing")
	}
}

func TestDaemonEnv_MainProcessInjection(t *testing.T) {
	gw := (&Gateway{}).WithDaemonCommand([]string{"kape-daemon"}).WithDaemonToken("t")
	env := gw.daemonEnv(map[string]string{LabelAgentID: "agent-1"}, "")
	if env["KAPE_DAEMON_TOKEN"] != "t" || env["KAPE_AGENT_ID"] != "agent-1" {
		t.Fatalf("token/agent env missing: %v", env)
	}
	if env["KAPE_SPOOL_PATH"] != "/sandbox/kape-daemon-spool.jsonl" {
		t.Fatal("PVC spool path env missing (main process has no profile hook)")
	}
	// The controller dials the sandbox POD IP, so a loopback bind (the daemon's
	// compiled default) is unreachable: the daemon must be told to bind the
	// configured port on all interfaces (kape#274 prompt path).
	if env["KAPE_DAEMON_ADDR"] != "0.0.0.0:9090" {
		t.Fatalf("KAPE_DAEMON_ADDR = %q, want 0.0.0.0:9090", env["KAPE_DAEMON_ADDR"])
	}
}

// TestDaemonEnv_HonoursConfiguredPort: the bind port tracks KAPE_DAEMON_PORT so
// the dial address and the bind address cannot drift.
func TestDaemonEnv_HonoursConfiguredPort(t *testing.T) {
	gw := (&Gateway{}).WithDaemonCommand([]string{"kape-daemon"}).WithDaemonPort("9191")
	if got := gw.daemonEnv(map[string]string{}, "")["KAPE_DAEMON_ADDR"]; got != "0.0.0.0:9191" {
		t.Fatalf("KAPE_DAEMON_ADDR = %q, want 0.0.0.0:9191", got)
	}
}

func TestDaemonEnv_SpoolPathOnlyWhenCommandThreaded(t *testing.T) {
	// Fallback posture: no command → no main-process env injection beyond
	// the D5 token channel (unchanged behavior).
	gw := (&Gateway{}).WithDaemonToken("t")
	env := gw.daemonEnv(map[string]string{}, "")
	if _, ok := env["KAPE_SPOOL_PATH"]; ok {
		t.Fatalf("KAPE_SPOOL_PATH injected without canonical command: %v", env)
	}
	if env["KAPE_DAEMON_TOKEN"] != "t" {
		t.Fatalf("token channel lost in fallback posture: %v", env)
	}
	// No token and no command → nil env, sandbox creation byte-identical.
	if env := (&Gateway{}).daemonEnv(map[string]string{}, ""); env != nil {
		t.Fatalf("bare gateway env = %v, want nil", env)
	}
}

func TestCreateSandboxRequest_EmptyCommandOmitted(t *testing.T) {
	// Fallback posture: empty configured command leaves the request without
	// Spec.Command so the gateway's own default main process applies.
	req := (&Gateway{}).createSandboxRequest("sbx-test", "img:1", "prov", Resources{CPU: "1", Memory: "1Gi"}, nil, nil, nil)
	if len(req.Spec.Command) != 0 {
		t.Fatalf("Spec.Command = %v, want omitted", req.Spec.Command)
	}
}

func TestCreateSandboxRequest_ProtoFieldNumber(t *testing.T) {
	// The vendored proto pins command to field 12 with the E2 wording; the
	// wire contract must not drift silently.
	req := (&Gateway{}).createSandboxRequest("sbx-test", "img", "prov", Resources{CPU: "1", Memory: "1Gi"}, nil, nil, []string{"kape-daemon"})
	md := req.Spec.ProtoReflect().Descriptor().Fields().ByJSONName("command")
	if md == nil {
		t.Fatal("SandboxSpec.command field missing from vendored proto")
	}
	if got := md.Number(); got != 12 {
		t.Fatalf("command field number = %d, want 12", got)
	}
}

func TestDaemonEnv_MergedSessionIDAndSpoolPath(t *testing.T) {
	// Merge of #255 (E3, issue #249) with #257 (E2, issue #247): the pinned
	// pi session id rides the D5 token channel (sandboxEnv), and the PVC
	// spool path is injected only in the canonical main-process posture.
	gw := (&Gateway{}).WithDaemonCommand([]string{"kape-daemon"}).WithDaemonToken("t")
	env := gw.daemonEnv(map[string]string{LabelAgentID: "agent-1"}, "kape-1")
	if env["KAPE_PI_SESSION_ID"] != "kape-1" {
		t.Fatalf("pinned id channel lost in main-process posture: %v", env)
	}
	if env["KAPE_SPOOL_PATH"] != "/sandbox/kape-daemon-spool.jsonl" {
		t.Fatalf("PVC spool path env missing alongside the pinned id: %v", env)
	}
	if env["KAPE_DAEMON_TOKEN"] != "t" || env["KAPE_AGENT_ID"] != "agent-1" {
		t.Fatalf("token/agent env missing: %v", env)
	}
}

// v0.1.0 mandates workspace_scope on every sandbox RPC ("workspace_scope is
// required"); the default workspace is the explicit name "default".

func TestWorkspaceSelector_DefaultsToExplicitDefaultName(t *testing.T) {
	// The default workspace is a name, never an omitted selector.
	if got := workspaceSelector("").GetWorkspace(); got != "default" {
		t.Fatalf("empty workspace selector = %q, want %q", got, "default")
	}
	if got := workspaceSelector("team-a").GetWorkspace(); got != "team-a" {
		t.Fatalf("named workspace selector = %q, want team-a", got)
	}
}

func TestCreateSandboxRequest_CarriesWorkspaceScope(t *testing.T) {
	req := (&Gateway{}).createSandboxRequest("sbx-test", "img", "prov",
		Resources{CPU: "1", Memory: "1Gi"}, nil, nil, nil)
	if got := req.GetWorkspaceScope().GetWorkspace(); got != defaultWorkspace {
		t.Fatalf("CreateSandbox workspace_scope = %q, want %q", got, defaultWorkspace)
	}
	if got := req.ProtoReflect().Descriptor().Fields().ByNumber(7); got == nil || got.Name() != "workspace_scope" {
		t.Fatalf("CreateSandboxRequest.workspace_scope field 7 missing from vendored proto: %v", got)
	}
}

func TestInteractiveExecRequest_AddressesSandboxByName(t *testing.T) {
	// v0.1.0 field 1 `sandbox` is the canonical sandbox name; the v0.0.116 pin
	// sent a sandbox UUID in the same slot.
	gw := (&Gateway{modelGatewayURL: "http://gw/v1"})
	req := gw.interactiveExecRequest("sbx-agent-1", "z-ai/glm-5.2")
	if req.GetSandbox() != "sbx-agent-1" {
		t.Fatalf("exec sandbox = %q, want the canonical name", req.GetSandbox())
	}
	if got := req.GetWorkspaceScope().GetWorkspace(); got != defaultWorkspace {
		t.Fatalf("exec workspace_scope = %q, want %q", got, defaultWorkspace)
	}
	if req.GetEnvironment()["PI_MODEL"] != "z-ai/glm-5.2" || req.GetWorkdir() != "/sandbox" {
		t.Fatalf("exec env/workdir lost: %v / %q", req.GetEnvironment(), req.GetWorkdir())
	}
}

// recordingClient records the request every Gateway RPC sends. The embedded
// OpenShellClient is nil, so an RPC the Gateway is not expected to call
// panics instead of silently passing.
type recordingClient struct {
	v1.OpenShellClient

	createSandbox *v1.CreateSandboxRequest
	getSandbox    *v1.GetSandboxRequest
	deleteSandbox *v1.DeleteSandboxRequest
	listProviders *v1.ListProvidersRequest
	listPages     []*v1.ListSandboxesRequest
	updateConfig  []*v1.UpdateConfigRequest
	execStarted   *v1.ExecSandboxRequest
	execOneShot   []*v1.ExecSandboxRequest
}

func (c *recordingClient) CreateSandbox(ctx context.Context, in *v1.CreateSandboxRequest, _ ...grpc.CallOption) (*v1.SandboxResponse, error) {
	c.createSandbox = in
	return &v1.SandboxResponse{Sandbox: &v1.Sandbox{Metadata: &v1.ObjectMeta{Name: "sbx-1", Id: "uuid-1"}}}, nil
}

func (c *recordingClient) GetSandbox(ctx context.Context, in *v1.GetSandboxRequest, _ ...grpc.CallOption) (*v1.SandboxResponse, error) {
	c.getSandbox = in
	return &v1.SandboxResponse{Sandbox: &v1.Sandbox{Status: &v1.SandboxStatus{Phase: v1.SandboxPhase_SANDBOX_PHASE_READY}}}, nil
}

func (c *recordingClient) DeleteSandbox(ctx context.Context, in *v1.DeleteSandboxRequest, _ ...grpc.CallOption) (*v1.DeleteSandboxResponse, error) {
	c.deleteSandbox = in
	return &v1.DeleteSandboxResponse{}, nil
}

func (c *recordingClient) ListProviders(ctx context.Context, in *v1.ListProvidersRequest, _ ...grpc.CallOption) (*v1.ListProvidersResponse, error) {
	c.listProviders = in
	return &v1.ListProvidersResponse{Providers: []*v1.Provider{{Metadata: &v1.ObjectMeta{Name: "openrouter"}}}}, nil
}

func (c *recordingClient) ListSandboxes(ctx context.Context, in *v1.ListSandboxesRequest, _ ...grpc.CallOption) (*v1.ListSandboxesResponse, error) {
	c.listPages = append(c.listPages, in)
	if in.GetPageToken() == "" {
		return &v1.ListSandboxesResponse{
			Sandboxes:     []*v1.Sandbox{{Metadata: &v1.ObjectMeta{Name: "sbx-1"}}},
			NextPageToken: "page-2",
		}, nil
	}
	return &v1.ListSandboxesResponse{Sandboxes: []*v1.Sandbox{{Metadata: &v1.ObjectMeta{Name: "sbx-2"}}}}, nil
}

func (c *recordingClient) UpdateConfig(ctx context.Context, in *v1.UpdateConfigRequest, _ ...grpc.CallOption) (*v1.UpdateConfigResponse, error) {
	c.updateConfig = append(c.updateConfig, in)
	return &v1.UpdateConfigResponse{}, nil
}

// ExecSandbox: one-shot exec (ProbeModelGateway). The probe returns the
// command's stdout as a single stream event so the caller drains cleanly.
type oneShotExecStream struct {
	grpc.ClientStream
	sent bool
}

func (s *oneShotExecStream) Recv() (*v1.ExecSandboxEvent, error) {
	if s.sent {
		return nil, io.EOF
	}
	s.sent = true
	return &v1.ExecSandboxEvent{
		Payload: &v1.ExecSandboxEvent_Stdout{Stdout: &v1.ExecSandboxStdout{Data: []byte("HTTP 200")}},
	}, nil
}

func (c *recordingClient) ExecSandbox(ctx context.Context, in *v1.ExecSandboxRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[v1.ExecSandboxEvent], error) {
	c.execOneShot = append(c.execOneShot, in)
	return &oneShotExecStream{}, nil
}

type recordingExecStream struct {
	grpc.ClientStream
	started *v1.ExecSandboxRequest
}

func (s *recordingExecStream) Send(in *v1.ExecSandboxInput) error {
	s.started = in.GetStart()
	return nil
}

func (s *recordingExecStream) Recv() (*v1.ExecSandboxEvent, error) { return nil, io.EOF }

func (c *recordingClient) ExecSandboxInteractive(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[v1.ExecSandboxInput, v1.ExecSandboxEvent], error) {
	return &recordingExecStream{}, nil
}

func TestGateway_SandboxRPCsCarryWorkspaceScope(t *testing.T) {
	cases := []struct {
		name      string
		workspace string
		want      string
	}{
		{"default", "", "default"},
		{"explicit default", "default", "default"},
		{"named override", "team-a", "team-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingClient{}
			gw := NewGateway(nil, "http://gw/v1").WithWorkspace(tc.workspace)
			gw.client = rec
			ctx := context.Background()

			if _, _, err := gw.CreateSandbox(ctx, "sbx-1", "img", "prov", Resources{CPU: "1", Memory: "1Gi"}, nil, ""); err != nil {
				t.Fatalf("CreateSandbox: %v", err)
			}
			if _, err := gw.GetSandboxPhase(ctx, "sbx-1"); err != nil {
				t.Fatalf("GetSandboxPhase: %v", err)
			}
			if _, err := gw.ListSandboxes(ctx, "managed-by=kape-controller"); err != nil {
				t.Fatalf("ListSandboxes: %v", err)
			}
			if err := gw.DeleteSandbox(ctx, "sbx-1"); err != nil {
				t.Fatalf("DeleteSandbox: %v", err)
			}
			if _, err := gw.ListProviders(ctx); err != nil {
				t.Fatalf("ListProviders: %v", err)
			}
			if err := gw.ApplyModelGatewayPolicy(ctx, "sbx-1"); err != nil {
				t.Fatalf("ApplyModelGatewayPolicy: %v", err)
			}
			if err := gw.ApplyControllerEgressDeny(ctx, "sbx-1", "controller.openshell.svc", 8081); err != nil {
				t.Fatalf("ApplyControllerEgressDeny: %v", err)
			}
			if _, err := gw.StartPi(ctx, "sbx-1", ""); err != nil {
				t.Fatalf("StartPi: %v", err)
			}

			for _, scope := range []string{
				rec.createSandbox.GetWorkspaceScope().GetWorkspace(),
				rec.getSandbox.GetWorkspaceScope().GetWorkspace(),
				rec.deleteSandbox.GetWorkspaceScope().GetWorkspace(),
				rec.listProviders.GetWorkspaceScope().GetWorkspace(),
			} {
				if scope != tc.want {
					t.Fatalf("workspace_scope = %q, want %q", scope, tc.want)
				}
			}
			for _, page := range rec.listPages {
				if got := page.GetWorkspaceScope().GetWorkspace(); got != tc.want {
					t.Fatalf("ListSandboxes workspace_scope = %q, want %q", got, tc.want)
				}
			}
			if len(rec.updateConfig) != 2 {
				t.Fatalf("UpdateConfig calls = %d, want 2 (model-gateway + controller deny)", len(rec.updateConfig))
			}
			for _, req := range rec.updateConfig {
				if got := req.GetWorkspaceScope().GetWorkspace(); got != tc.want {
					t.Fatalf("UpdateConfig workspace_scope = %q, want %q", got, tc.want)
				}
				if req.GetSandbox() != "sbx-1" {
					t.Fatalf("UpdateConfig sandbox = %q, want sbx-1", req.GetSandbox())
				}
			}
		})
	}
}

func TestGateway_ListSandboxesPagesByToken(t *testing.T) {
	// v0.1.0 replaced limit/offset with page_size/page_token; the registry
	// must follow next_page_token to the end.
	rec := &recordingClient{}
	gw := NewGateway(nil, "http://gw/v1")
	gw.client = rec
	got, err := gw.ListSandboxes(context.Background(), "managed-by=kape-controller")
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(got) != 2 || got[0].Name != "sbx-1" || got[1].Name != "sbx-2" {
		t.Fatalf("paged sandboxes = %+v, want sbx-1 then sbx-2", got)
	}
	if len(rec.listPages) != 2 {
		t.Fatalf("ListSandboxes calls = %d, want 2 pages", len(rec.listPages))
	}
	if rec.listPages[0].GetPageSize() != 100 || rec.listPages[0].GetPageToken() != "" {
		t.Fatalf("first page = size %d token %q, want 100/empty", rec.listPages[0].GetPageSize(), rec.listPages[0].GetPageToken())
	}
	if rec.listPages[1].GetPageToken() != "page-2" {
		t.Fatalf("second page token = %q, want page-2", rec.listPages[1].GetPageToken())
	}
	if rec.listPages[0].GetLabelSelector() != "managed-by=kape-controller" {
		t.Fatalf("label selector lost: %q", rec.listPages[0].GetLabelSelector())
	}
}

func TestGateway_ControllerEgressDenyTargetsAllowEndpoint(t *testing.T) {
	// v0.1.0 types the deny target (rule, host, ports, binary scope) instead of
	// bare host/port; an omitted or mismatched scope is rejected by the gateway.
	rec := &recordingClient{}
	gw := NewGateway(nil, "http://gw/v1")
	gw.client = rec
	if err := gw.ApplyControllerEgressDeny(context.Background(), "sbx-1", "controller.openshell.svc", 8081); err != nil {
		t.Fatalf("ApplyControllerEgressDeny: %v", err)
	}
	ops := rec.updateConfig[0].GetMergeOperations()
	deny := ops[1].GetAddDenyRules()
	if deny == nil {
		t.Fatalf("second merge op is not add_deny_rules: %v", ops[1])
	}
	target := deny.GetTarget()
	if target.GetRuleName() != "m1_controller" || target.GetHost() != "controller.openshell.svc" {
		t.Fatalf("deny target = %v", target)
	}
	if ports := target.GetPorts(); len(ports) != 1 || ports[0] != 8081 {
		t.Fatalf("deny target ports = %v, want [8081]", ports)
	}
	if !target.GetAnyBinary() {
		t.Fatal("deny target must declare any_binary to match the allow endpoint (no rule binaries)")
	}
	if len(target.GetBinaries()) != 0 {
		t.Fatalf("deny target declared binaries alongside any_binary: %v", target.GetBinaries())
	}
}

// TestProbeModelGateway_UsesConfiguredEndpoint: the startup probe targets the
// same gateway the pi wrapper patches models.json to. A compiled-in probe host
// would report on an endpoint the sandbox is not configured to reach.
func TestProbeModelGateway_UsesConfiguredEndpoint(t *testing.T) {
	rec := &recordingClient{}
	gw := NewGateway(nil, "https://gateway.example.com:8443/openai/v1")
	gw.client = rec

	gw.ProbeModelGateway(context.Background(), "sbx-1")

	if len(rec.execOneShot) != 1 {
		t.Fatalf("ExecSandbox calls = %d, want exactly 1 probe", len(rec.execOneShot))
	}
	if got := rec.execOneShot[0].GetSandbox(); got != "sbx-1" {
		t.Fatalf("probe sandbox = %q, want sbx-1", got)
	}
	cmd := strings.Join(rec.execOneShot[0].GetCommand(), " ")
	if want := "https://gateway.example.com:8443/openai/v1/models"; !strings.Contains(cmd, want) {
		t.Fatalf("probe command = %q, want it to GET %s", cmd, want)
	}
}

// TestCreateSandboxRequest_AttachesCredentialProviders is the kape#274 AC2/AC3
// guard: NetworkCredentialBinding.provider must be "attached to this sandbox"
// or the egress proxy cannot resolve a credential, so the binding providers
// ride the same CreateSandbox request as the model provider.
func TestCreateSandboxRequest_AttachesCredentialProviders(t *testing.T) {
	gw := (&Gateway{}).
		WithCredentialBindings([]Binding{
			{Provider: "kape-kape-system-default-github", Hosts: []string{"github.com:443"}},
			{Provider: "another-cred", Hosts: []string{"git.example.com:443"}},
		})
	req := gw.createSandboxRequest("sbx-1", "img", "openrouter", Resources{CPU: "1", Memory: "1Gi"}, nil, nil, nil)
	got := req.GetSpec().GetProviders()
	want := []string{"openrouter", "kape-kape-system-default-github", "another-cred"}
	if len(got) != len(want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("providers = %v, want %v", got, want)
		}
	}

	// No bindings: byte-identical to the pre-#264 posture.
	bare := (&Gateway{}).createSandboxRequest("sbx-1", "img", "openrouter", Resources{CPU: "1", Memory: "1Gi"}, nil, nil, nil)
	if p := bare.GetSpec().GetProviders(); len(p) != 1 || p[0] != "openrouter" {
		t.Fatalf("providers without bindings = %v, want [openrouter]", p)
	}

	// A binding naming the model provider must not duplicate it.
	dup := (&Gateway{}).
		WithCredentialBindings([]Binding{{Provider: "openrouter", Hosts: []string{"x:443"}}}).
		createSandboxRequest("sbx-1", "img", "openrouter", Resources{CPU: "1", Memory: "1Gi"}, nil, nil, nil)
	if p := dup.GetSpec().GetProviders(); len(p) != 1 {
		t.Fatalf("providers = %v, want [openrouter] with no duplicate", p)
	}
}
