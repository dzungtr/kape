package main

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	v1 "github.com/dzungtr/kape/controller/gen"
)

// Issue #264 (ADR-0088): KAPE_CREDENTIAL_BINDINGS parse + additive-only policy
// merge. Handoff H1 pins the env JSON schema to [{"provider","hosts"}] with
// host:port patterns (DNS globs allowed) — these tests are the executable
// statement of that contract.

// testModelGatewayURL is the endpoint the policy tests derive the model-gateway
// rule from. The rule carries no compiled-in endpoint: it is always derived
// from the configured KAPE_MODEL_GATEWAY_URL.
const testModelGatewayURL = "http://gw.kape-system.svc.cluster.local:80/v1"

// mustModelGatewayRule builds the model-gateway rule for the test endpoint.
func mustModelGatewayRule(t *testing.T) *v1.NetworkPolicyRule {
	t.Helper()
	rule, err := modelGatewayRule(testModelGatewayURL)
	if err != nil {
		t.Fatalf("modelGatewayRule(%q): %v", testModelGatewayURL, err)
	}
	return rule
}

// newPolicyTestGateway is a Gateway wired for the policy-merge tests: only the
// stubbed UpdateConfig is reachable, and the configured gateway URL is the one
// the rule is derived from.
func newPolicyTestGateway(client v1.OpenShellClient) *Gateway {
	return &Gateway{client: client, modelGatewayURL: testModelGatewayURL}
}

func TestParseBindings(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []Binding
		wantErr string
	}{
		{
			name: "empty value means no bindings",
			raw:  "",
		},
		{
			name: "blank value means no bindings",
			raw:  "  \n\t ",
		},
		{
			name: "single provider two hosts",
			raw:  `[{"provider":"kape-openshell-kape-github","hosts":["github.com:443","api.github.com:443"]}]`,
			want: []Binding{{Provider: "kape-openshell-kape-github", Hosts: []string{"github.com:443", "api.github.com:443"}}},
		},
		{
			name: "two providers",
			raw:  `[{"provider":"p1","hosts":["github.com:443"]},{"provider":"p2","hosts":["gitlab.com:443"]}]`,
			want: []Binding{{Provider: "p1", Hosts: []string{"github.com:443"}}, {Provider: "p2", Hosts: []string{"gitlab.com:443"}}},
		},
		{
			name:    "not json",
			raw:     `provider=github`,
			wantErr: "KAPE_CREDENTIAL_BINDINGS",
		},
		{
			name:    "wrong shape",
			raw:     `{"provider":"p1","hosts":["github.com:443"]}`,
			wantErr: "KAPE_CREDENTIAL_BINDINGS",
		},
		{
			name:    "unknown field",
			raw:     `[{"provider":"p1","hosts":["github.com:443"],"on_error":"fail_closed"}]`,
			wantErr: "unknown field",
		},
		{
			name:    "trailing data",
			raw:     `[{"provider":"p1","hosts":["github.com:443"]}] []`,
			wantErr: "trailing data",
		},
		{
			name:    "missing provider",
			raw:     `[{"hosts":["github.com:443"]}]`,
			wantErr: "provider",
		},
		{
			name:    "provider with invalid characters",
			raw:     `[{"provider":"kape/openshell","hosts":["github.com:443"]}]`,
			wantErr: "provider",
		},
		{
			name:    "no hosts",
			raw:     `[{"provider":"p1","hosts":[]}]`,
			wantErr: "at least one host pattern",
		},
		{
			name:    "host without port",
			raw:     `[{"provider":"p1","hosts":["github.com"]}]`,
			wantErr: "must be host:port",
		},
		{
			name:    "host with empty port",
			raw:     `[{"provider":"p1","hosts":["github.com:"]}]`,
			wantErr: "must be host:port",
		},
		{
			name:    "host with non-numeric port",
			raw:     `[{"provider":"p1","hosts":["github.com:https"]}]`,
			wantErr: "must be an integer",
		},
		{
			name:    "host with out-of-range port",
			raw:     `[{"provider":"p1","hosts":["github.com:70000"]}]`,
			wantErr: "must be an integer in 1..65535",
		},
		{
			name:    "host with path",
			raw:     `[{"provider":"p1","hosts":["github.com/org/repo:443"]}]`,
			wantErr: "DNS name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseBindings(tc.raw)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseBindings(%q) = %v, want error containing %q", tc.raw, got, tc.wantErr)
				}
				if !contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				if got != nil {
					t.Fatalf("bindings = %v on error, want nil (fail closed)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseBindings(%q): %v", tc.raw, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("bindings = %+v, want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i].Provider != tc.want[i].Provider {
					t.Fatalf("binding[%d].provider = %q, want %q", i, got[i].Provider, tc.want[i].Provider)
				}
				if len(got[i].Hosts) != len(tc.want[i].Hosts) {
					t.Fatalf("binding[%d].hosts = %v, want %v", i, got[i].Hosts, tc.want[i].Hosts)
				}
				for j := range tc.want[i].Hosts {
					if got[i].Hosts[j] != tc.want[i].Hosts[j] {
						t.Fatalf("binding[%d].hosts[%d] = %q, want %q (host patterns pass through verbatim)", i, j, got[i].Hosts[j], tc.want[i].Hosts[j])
					}
				}
			}
		})
	}
}

// TestParseBindings_HostGlobPassthrough: DNS globs are the gateway's to
// interpret — the controller never expands or rewrites them.
func TestParseBindings_HostGlobPassthrough(t *testing.T) {
	raw := `[{"provider":"p1","hosts":["*.github.com:443","**.github.com:8443"]}]`
	bindings, err := ParseBindings(raw)
	if err != nil {
		t.Fatalf("ParseBindings: %v", err)
	}
	merged, err := MergePolicy(&v1.NetworkPolicyRule{Name: CredentialBindingRuleName}, bindings)
	if err != nil {
		t.Fatalf("MergePolicy: %v", err)
	}
	want := []struct {
		host string
		port uint32
	}{{"*.github.com", 443}, {"**.github.com", 8443}}
	if len(merged.Endpoints) != len(want) {
		t.Fatalf("endpoints = %v, want %d", merged.Endpoints, len(want))
	}
	for i, w := range want {
		ep := merged.Endpoints[i]
		if ep.GetHost() != w.host || ep.GetPort() != w.port {
			t.Fatalf("endpoint[%d] = %s:%d, want %s:%d (glob passthrough)", i, ep.GetHost(), ep.GetPort(), w.host, w.port)
		}
	}
}

// TestMergePolicy_AdditiveOnly pins the #264 invariant: the model-gateway
// egress rule survives the merge byte-identical — same endpoints, same
// binaries, same order — and the input is not mutated.
func TestMergePolicy_AdditiveOnly(t *testing.T) {
	gatewayRule := mustModelGatewayRule(t)
	original := proto.Clone(gatewayRule).(*v1.NetworkPolicyRule)

	merged, err := MergePolicy(gatewayRule, []Binding{{
		Provider: "kape-openshell-kape-github",
		Hosts:    []string{"github.com:443", "api.github.com:443"},
	}})
	if err != nil {
		t.Fatalf("MergePolicy: %v", err)
	}
	if !proto.Equal(gatewayRule, original) {
		t.Fatalf("MergePolicy mutated its input policy: got %v, want %v", gatewayRule, original)
	}
	if merged.GetName() != gatewayRule.GetName() {
		t.Fatalf("rule name = %q, want %q (nothing renamed)", merged.GetName(), gatewayRule.GetName())
	}
	if len(merged.Endpoints) <= len(original.Endpoints) {
		t.Fatalf("endpoints = %d, want the %d originals plus bound endpoints", len(merged.Endpoints), len(original.Endpoints))
	}
	for i, want := range original.Endpoints {
		if !proto.Equal(merged.Endpoints[i], want) {
			t.Fatalf("endpoint[%d] = %v, want the original %v preserved verbatim", i, merged.Endpoints[i], want)
		}
	}
	// Binaries: the originals first and unchanged, then the binding additions.
	for i, want := range original.Binaries {
		if !proto.Equal(merged.Binaries[i], want) {
			t.Fatalf("binary[%d] = %v, want the original %v preserved verbatim", i, merged.Binaries[i], want)
		}
	}
	// The model-gateway endpoint keeps its L7 rules: no weakening.
	for _, ep := range merged.Endpoints {
		if ep.GetHost() == original.Endpoints[0].GetHost() {
			if len(ep.GetRules()) != 1 || ep.GetRules()[0].GetAllow().GetPath() != "**" {
				t.Fatalf("model-gateway endpoint lost its L7 allow: %v", ep)
			}
			return
		}
	}
	t.Fatal("model-gateway endpoint missing from the merged policy")
}

// TestMergePolicy_BoundEndpoints: each binding host pattern becomes one REST
// endpoint carrying credential_binding = provider.
func TestMergePolicy_BoundEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		bindings []Binding
		want     []struct {
			host   string
			port   uint32
			provid string
		}
	}{
		{
			name: "one provider two hosts",
			bindings: []Binding{{
				Provider: "p-git",
				Hosts:    []string{"github.com:443", "api.github.com:443"},
			}},
			want: []struct {
				host   string
				port   uint32
				provid string
			}{{"github.com", 443, "p-git"}, {"api.github.com", 443, "p-git"}},
		},
		{
			name: "two providers keep their own bindings",
			bindings: []Binding{
				{Provider: "p-git", Hosts: []string{"github.com:443"}},
				{Provider: "p-registry", Hosts: []string{"registry.example.com:5000"}},
			},
			want: []struct {
				host   string
				port   uint32
				provid string
			}{{"github.com", 443, "p-git"}, {"registry.example.com", 5000, "p-registry"}},
		},
		{
			name: "duplicate host patterns merge once, first binding wins",
			bindings: []Binding{
				{Provider: "p-git", Hosts: []string{"github.com:443"}},
				{Provider: "p-other", Hosts: []string{"GitHub.com:443"}},
			},
			want: []struct {
				host   string
				port   uint32
				provid string
			}{{"github.com", 443, "p-git"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := MergePolicy(&v1.NetworkPolicyRule{Name: CredentialBindingRuleName}, tc.bindings)
			if err != nil {
				t.Fatalf("MergePolicy: %v", err)
			}
			if len(merged.Endpoints) != len(tc.want) {
				t.Fatalf("endpoints = %v, want %d", merged.Endpoints, len(tc.want))
			}
			for i, w := range tc.want {
				ep := merged.Endpoints[i]
				if ep.GetHost() != w.host || ep.GetPort() != w.port {
					t.Fatalf("endpoint[%d] = %s:%d, want %s:%d", i, ep.GetHost(), ep.GetPort(), w.host, w.port)
				}
				if ep.GetProtocol() != "rest" {
					t.Fatalf("endpoint[%d].protocol = %q, want \"rest\" (L7 inspection for credential injection)", i, ep.GetProtocol())
				}
				// The gateway rejects a "rest" endpoint with no allowed
				// traffic (verified live on v0.1.0), so every bound endpoint
				// must carry the L7 allow rule.
				if len(ep.GetRules()) != 1 || ep.GetRules()[0].GetAllow().GetMethod() != "*" || ep.GetRules()[0].GetAllow().GetPath() != "**" {
					t.Fatalf("endpoint[%d].rules = %v, want one allow * /** (gateway requires allowed traffic)", i, ep.GetRules())
				}
				if got := ep.GetCredentialBinding().GetProvider(); got != w.provid {
					t.Fatalf("endpoint[%d].credential_binding.provider = %q, want %q", i, got, w.provid)
				}
			}
		})
	}
}

// TestMergePolicy_BinariesAllowlist: bound endpoints allowlist git/gh/pi —
// OPA denies egress for unlisted binaries (M1 spike finding) — exactly once
// each, even when the merged rule already allowlists one of them.
func TestMergePolicy_BinariesAllowlist(t *testing.T) {
	// The model-gateway rule already allowlists /usr/bin/pi: the binding
	// additions must not duplicate it.
	merged, err := MergePolicy(mustModelGatewayRule(t), []Binding{{Provider: "p-git", Hosts: []string{"github.com:443"}}})
	if err != nil {
		t.Fatalf("MergePolicy: %v", err)
	}
	counts := map[string]int{}
	for _, b := range merged.Binaries {
		counts[b.GetPath()]++
	}
	for _, path := range []string{"/usr/bin/git", "/usr/bin/gh", "/usr/bin/pi"} {
		if counts[path] != 1 {
			t.Fatalf("binary %s appears %d times in %v, want exactly once", path, counts[path], merged.Binaries)
		}
	}
	// The original binaries stay allowlisted.
	for _, path := range []string{"/usr/bin/curl", "/usr/bin/node"} {
		if counts[path] != 1 {
			t.Fatalf("original binary %s lost by the merge: %v", path, merged.Binaries)
		}
	}
}

// TestMergePolicy_NoBindingsLeavesPolicyUnchanged: with no bindings the merge
// is a no-op copy, so the pre-#264 policy stays byte-identical.
func TestMergePolicy_NoBindingsLeavesPolicyUnchanged(t *testing.T) {
	original := mustModelGatewayRule(t)
	merged, err := MergePolicy(original, nil)
	if err != nil {
		t.Fatalf("MergePolicy: %v", err)
	}
	if !proto.Equal(merged, original) {
		t.Fatalf("merged = %v, want the policy unchanged", merged)
	}
}

// TestMergePolicy_FailClosed: an unmaterialisable binding is an error and no
// rule at all — never a partial policy.
func TestMergePolicy_FailClosed(t *testing.T) {
	tests := []struct {
		name     string
		bindings []Binding
		wantErr  string
	}{
		{
			name:     "missing port",
			bindings: []Binding{{Provider: "p-git", Hosts: []string{"github.com"}}},
			wantErr:  "p-git",
		},
		{
			name:     "empty host",
			bindings: []Binding{{Provider: "p-git", Hosts: []string{":443"}}},
			wantErr:  "p-git",
		},
		{
			name:     "port out of range",
			bindings: []Binding{{Provider: "p-git", Hosts: []string{"github.com:0"}}},
			wantErr:  "p-git",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := MergePolicy(&v1.NetworkPolicyRule{Name: CredentialBindingRuleName}, tc.bindings)
			if err == nil {
				t.Fatalf("MergePolicy = %v, want an error naming the provider", merged)
			}
			if merged != nil {
				t.Fatalf("MergePolicy returned %v with an error, want nil (no half-applied policy)", merged)
			}
			if !contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to name %q", err, tc.wantErr)
			}
		})
	}
	if _, err := MergePolicy(nil, []Binding{{Provider: "p-git", Hosts: []string{"github.com:443"}}}); err == nil {
		t.Fatal("MergePolicy(nil, ...) = nil error, want failure")
	}
}

// stubPolicyClient records UpdateConfig calls; embedding the generated client
// interface leaves every other RPC unusable, which is what a policy-merge test
// wants (nothing else may be called).
type stubPolicyClient struct {
	v1.OpenShellClient
	requests []*v1.UpdateConfigRequest
	err      error
	// policy is what GetSandboxConfig reports as the sandbox's current policy
	// (nil = an empty policy: no rule declares any endpoint).
	policy *v1.SandboxPolicy
	// configErr makes GetSandboxConfig fail (fail-closed coverage).
	configErr error
}

func (s *stubPolicyClient) GetSandboxConfig(ctx context.Context, req *v1.GetSandboxConfigRequest, opts ...grpc.CallOption) (*v1.GetSandboxConfigResponse, error) {
	if s.configErr != nil {
		return nil, s.configErr
	}
	policy := s.policy
	if policy == nil {
		policy = &v1.SandboxPolicy{}
	}
	return &v1.GetSandboxConfigResponse{Policy: policy}, nil
}

func (s *stubPolicyClient) UpdateConfig(ctx context.Context, req *v1.UpdateConfigRequest, opts ...grpc.CallOption) (*v1.UpdateConfigResponse, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return nil, s.err
	}
	return &v1.UpdateConfigResponse{}, nil
}

// TestApplyModelGatewayPolicy_MergesCredentials: the seam sends the untouched
// model-gateway rule plus one credential-binding rule, in a single update.
func TestApplyModelGatewayPolicy_MergesCredentials(t *testing.T) {
	client := &stubPolicyClient{}
	gw := newPolicyTestGateway(client).WithCredentialBindings([]Binding{{
		Provider: "kape-openshell-kape-github",
		Hosts:    []string{"github.com:443", "api.github.com:443"},
	}})

	if err := gw.ApplyModelGatewayPolicy(context.Background(), "sbx-1"); err != nil {
		t.Fatalf("ApplyModelGatewayPolicy: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("UpdateConfig calls = %d, want 1", len(client.requests))
	}
	req := client.requests[0]
	if req.GetSandbox() != "sbx-1" {
		t.Fatalf("request sandbox = %q, want sbx-1 (exec-by-canonical-name)", req.GetSandbox())
	}
	if got := req.GetWorkspaceScope().GetWorkspace(); got != defaultWorkspace {
		t.Fatalf("workspace_scope = %q, want %q", got, defaultWorkspace)
	}
	if len(req.GetMergeOperations()) != 2 {
		t.Fatalf("merge operations = %d, want 2 (model gateway + credential binding)", len(req.GetMergeOperations()))
	}
	gatewayAdd := req.GetMergeOperations()[0].GetAddRule()
	if gatewayAdd.GetRuleName() != "m1_model_gateway" {
		t.Fatalf("op[0] rule = %q, want m1_model_gateway", gatewayAdd.GetRuleName())
	}
	if !proto.Equal(gatewayAdd.GetRule(), mustModelGatewayRule(t)) {
		t.Fatalf("op[0] rule = %v, want the model-gateway rule unchanged", gatewayAdd.GetRule())
	}
	bindingAdd := req.GetMergeOperations()[1].GetAddRule()
	if bindingAdd.GetRuleName() != CredentialBindingRuleName {
		t.Fatalf("op[1] rule = %q, want %q", bindingAdd.GetRuleName(), CredentialBindingRuleName)
	}
	if got := len(bindingAdd.GetRule().GetEndpoints()); got != 2 {
		t.Fatalf("binding endpoints = %d, want 2", got)
	}
	for _, ep := range bindingAdd.GetRule().GetEndpoints() {
		if ep.GetCredentialBinding().GetProvider() != "kape-openshell-kape-github" {
			t.Fatalf("endpoint %s:%d lost its credential binding: %v", ep.GetHost(), ep.GetPort(), ep)
		}
	}
}

// TestApplyModelGatewayPolicy_StampsImageDeclaredEndpoints is the kape#274
// regression: when the sandbox policy already declares the bound hosts (every
// pi-openshell sandbox does), the seam stamps those rules and must NOT append a
// second declaration of the same host:port, which the gateway rejects as
// ambiguous credential metadata.
func TestApplyModelGatewayPolicy_StampsImageDeclaredEndpoints(t *testing.T) {
	client := &stubPolicyClient{policy: imagePolicyDeclaringBoundHosts()}
	gw := newPolicyTestGateway(client).WithCredentialBindings(testGithubBindings())

	if err := gw.ApplyModelGatewayPolicy(context.Background(), "sbx-1"); err != nil {
		t.Fatalf("ApplyModelGatewayPolicy: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("UpdateConfig calls = %d, want 1", len(client.requests))
	}
	ops := client.requests[0].GetMergeOperations()
	if got, want := len(ops), 4; got != want { // model gateway + 3 declaring rules
		names := []string{}
		for _, op := range ops {
			names = append(names, op.GetAddRule().GetRuleName())
		}
		t.Fatalf("merge operations = %d %v, want %d", got, names, want)
	}
	for _, op := range ops[1:] {
		if op.GetAddRule().GetRuleName() == CredentialBindingRuleName {
			t.Fatal("additive credential-binding rule emitted despite the policy already declaring every bound host")
		}
	}
}

// TestApplyModelGatewayPolicy_FailClosedOnUnreadablePolicy: a policy that
// cannot be read aborts before any merge is sent.
func TestApplyModelGatewayPolicy_FailClosedOnUnreadablePolicy(t *testing.T) {
	client := &stubPolicyClient{configErr: fmt.Errorf("permission denied")}
	gw := newPolicyTestGateway(client).WithCredentialBindings(testGithubBindings())

	err := gw.ApplyModelGatewayPolicy(context.Background(), "sbx-1")
	if err == nil {
		t.Fatal("ApplyModelGatewayPolicy succeeded with an unreadable policy, want failure")
	}
	if len(client.requests) != 0 {
		t.Fatalf("UpdateConfig called %d times, want 0 (fail closed before any merge)", len(client.requests))
	}
}

// TestApplyModelGatewayPolicy_NoBindingsKeepsModelGatewayOnly: an empty table
// keeps the pre-#264 request shape exactly.
func TestApplyModelGatewayPolicy_NoBindingsKeepsModelGatewayOnly(t *testing.T) {
	client := &stubPolicyClient{}
	gw := newPolicyTestGateway(client)
	if err := gw.ApplyModelGatewayPolicy(context.Background(), "sbx-1"); err != nil {
		t.Fatalf("ApplyModelGatewayPolicy: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("UpdateConfig calls = %d, want 1", len(client.requests))
	}
	if got := len(client.requests[0].GetMergeOperations()); got != 1 {
		t.Fatalf("merge operations = %d, want 1 (model gateway only)", got)
	}
}

// TestApplyModelGatewayPolicy_FailClosedOnBadBinding: a binding that cannot be
// materialised aborts before the gateway is called — no half-applied policy.
func TestApplyModelGatewayPolicy_FailClosedOnBadBinding(t *testing.T) {
	client := &stubPolicyClient{}
	gw := newPolicyTestGateway(client).WithCredentialBindings([]Binding{{Provider: "p-git", Hosts: []string{"github.com"}}})

	err := gw.ApplyModelGatewayPolicy(context.Background(), "sbx-1")
	if err == nil {
		t.Fatal("ApplyModelGatewayPolicy succeeded with a malformed binding, want error")
	}
	if !contains(err.Error(), "credential binding policy") || !contains(err.Error(), "p-git") {
		t.Fatalf("error = %q, want it to name the credential binding and provider", err)
	}
	if len(client.requests) != 0 {
		t.Fatalf("UpdateConfig called %d times, want 0 (fail closed before any merge)", len(client.requests))
	}
}

// TestApplyModelGatewayPolicy_FailClosedOnGatewayRejection: the gateway
// rejecting the bound endpoint (e.g. an unknown provider) surfaces as an error
// carrying the gateway's diagnostics.
func TestApplyModelGatewayPolicy_FailClosedOnGatewayRejection(t *testing.T) {
	client := &stubPolicyClient{err: fmt.Errorf(`provider "kape-openshell-kape-missing" is not attached to sandbox`)}
	gw := newPolicyTestGateway(client).WithCredentialBindings([]Binding{{
		Provider: "kape-openshell-kape-missing",
		Hosts:    []string{"github.com:443"},
	}})

	err := gw.ApplyModelGatewayPolicy(context.Background(), "sbx-1")
	if err == nil {
		t.Fatal("ApplyModelGatewayPolicy succeeded despite gateway rejection, want error")
	}
	if !contains(err.Error(), "UpdateConfig") || !contains(err.Error(), "not attached") {
		t.Fatalf("error = %q, want the gateway rejection surfaced", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("UpdateConfig calls = %d, want 1 (single atomic merge, no retry)", len(client.requests))
	}
}

// TestCreateRollsBackSandboxWhenBindingPolicyRejected: a rejected
// credential-binding merge during Create deletes the just-created sandbox and
// fails the Create with the named error (agent.go rollback path, #264 AC).
func TestCreateRollsBackSandboxWhenBindingPolicyRejected(t *testing.T) {
	gw := NewFakeGateway()
	gw.SetStream(NewFakeStream())
	gw.FailModelGatewayPolicy(fmt.Errorf("credential binding p-git: provider is not attached"))
	mgr := NewAgentManager(gw, testConfig(), testStore(t))

	_, err := mgr.Create(context.Background(), nil)
	if err == nil {
		t.Fatal("Create succeeded despite a rejected binding policy, want error")
	}
	if !contains(err.Error(), "apply network policy") || !contains(err.Error(), "credential binding") {
		t.Fatalf("error = %q, want it wrapped as apply network policy with the binding cause", err)
	}
	deleted := gw.DeletedSandboxes()
	if len(deleted) != 1 {
		t.Fatalf("deleted sandboxes = %v, want one rollback delete", deleted)
	}
	if len(gw.ControllerDenies()) != 0 {
		t.Fatal("egress deny applied after the policy apply failed, want an aborted Create")
	}
}

// --- Issue #274: stamping into an image policy that already declares the
// bound endpoints ---------------------------------------------------------
//
// pi-openshell declares github.com:443 and api.github.com:443. Appending the
// additive m1_credential_bindings rule next to them made the gateway reject the
// whole merge as ambiguous credential metadata, which rolled the sandbox back
// after it reached READY. These fixtures reproduce that policy shape.

func imagePolicyDeclaringBoundHosts() *v1.SandboxPolicy {
	return &v1.SandboxPolicy{
		NetworkPolicies: map[string]*v1.NetworkPolicyRule{
			"github_rest_api": {
				Name: "github-rest-api",
				Endpoints: []*v1.NetworkEndpoint{{
					Host:     "api.github.com",
					Port:     443,
					Protocol: "rest",
					Access:   v1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_READ_ONLY,
				}},
				Binaries: []*v1.NetworkBinary{{Path: "/usr/bin/gh"}},
			},
			"github_ssh_over_https": {
				Name: "github-ssh-over-https",
				Endpoints: []*v1.NetworkEndpoint{{
					Host:     "github.com",
					Port:     443,
					Protocol: "rest",
					Rules: []*v1.L7Rule{
						{Allow: &v1.L7Allow{Method: "GET", Path: "/**/info/refs*"}},
						{Allow: &v1.L7Allow{Method: "POST", Path: "/**/git-upload-pack"}},
					},
				}},
				Binaries: []*v1.NetworkBinary{{Path: "/usr/bin/git"}},
			},
			// Same host:port as github_ssh_over_https, bare. Left unstamped it
			// would recreate the ambiguity against the stamped rule.
			"pi": {
				Name:      "pi",
				Endpoints: []*v1.NetworkEndpoint{{Host: "github.com", Port: 443}},
				Binaries:  []*v1.NetworkBinary{{Path: "/usr/local/bin/pi"}},
			},
			"pypi": {
				Name:      "pypi",
				Endpoints: []*v1.NetworkEndpoint{{Host: "pypi.org", Port: 443}},
				Binaries:  []*v1.NetworkBinary{{Path: "/sandbox/.venv/bin/pip"}},
			},
		},
	}
}

func testGithubBindings() []Binding {
	return []Binding{{Provider: "kape-kape-system-default-github", Hosts: []string{"github.com:443", "api.github.com:443"}}}
}

// TestCredentialPolicyOps_StampsEveryDeclaringRule: no additive rule is emitted
// for hosts the policy already declares; each declaring rule is re-sent with
// the binding stamped and everything else preserved.
func TestCredentialPolicyOps_StampsEveryDeclaringRule(t *testing.T) {
	policy := imagePolicyDeclaringBoundHosts()
	ops, err := CredentialPolicyOps(policy, testGithubBindings())
	if err != nil {
		t.Fatalf("CredentialPolicyOps: %v", err)
	}
	byName := map[string]*v1.NetworkPolicyRule{}
	for _, op := range ops {
		add := op.GetAddRule()
		if add == nil {
			t.Fatalf("unexpected operation %T, want add_rule", op.GetOperation())
		}
		byName[add.GetRuleName()] = add.GetRule()
	}
	// Ops address rules by POLICY KEY: that is what the gateway upserts on.
	for _, want := range []string{"github_rest_api", "github_ssh_over_https", "pi"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("rule %q not stamped; ops = %v", want, keysOf(byName))
		}
	}
	if _, ok := byName[CredentialBindingRuleName]; ok {
		t.Fatal("additive m1_credential_bindings emitted for hosts the policy already declares")
	}
	if _, ok := byName["pypi"]; ok {
		t.Fatal("unbound rule pypi rewritten, want untouched")
	}

	// The read-only github constraints survive the stamp.
	ssh := byName["github_ssh_over_https"]
	if got := ssh.GetName(); got != "github-ssh-over-https" {
		t.Fatalf("rule body name = %q, want the policy's own name preserved", got)
	}
	if got := len(ssh.GetEndpoints()); got != 1 {
		t.Fatalf("github-ssh-over-https endpoints = %d, want 1", got)
	}
	ep := ssh.GetEndpoints()[0]
	if got := ep.GetCredentialBinding().GetProvider(); got != "kape-kape-system-default-github" {
		t.Fatalf("credential_binding.provider = %q", got)
	}
	if got := len(ep.GetRules()); got != 2 {
		t.Fatalf("L7 rules = %d, want 2 (read-only git smart-HTTP preserved)", got)
	}
	if got := ssh.GetBinaries(); len(got) != 1 || got[0].GetPath() != "/usr/bin/git" {
		t.Fatalf("binaries = %v, want the rule's own /usr/bin/git", got)
	}

	// access preset preserved on the REST endpoint.
	rest := byName["github_rest_api"].GetEndpoints()[0]
	if rest.GetAccess() != v1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_READ_ONLY {
		t.Fatalf("access = %v, want read-only preserved", rest.GetAccess())
	}

	// Input policy is never mutated.
	if imagePolicyDeclaringBoundHosts().GetNetworkPolicies()["github_ssh_over_https"].GetEndpoints()[0].GetCredentialBinding() != nil {
		t.Fatal("CredentialPolicyOps mutated its input policy")
	}
}

// TestCredentialPolicyOps_FallsBackToAdditiveRule: a bound host the policy does
// not declare keeps the original additive behaviour.
func TestCredentialPolicyOps_FallsBackToAdditiveRule(t *testing.T) {
	ops, err := CredentialPolicyOps(&v1.SandboxPolicy{}, []Binding{
		{Provider: "p-git", Hosts: []string{"git.example.com:443"}},
	})
	if err != nil {
		t.Fatalf("CredentialPolicyOps: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops = %d, want 1 additive rule", len(ops))
	}
	add := ops[0].GetAddRule()
	if add.GetRuleName() != CredentialBindingRuleName {
		t.Fatalf("rule name = %q", add.GetRuleName())
	}
	ep := add.GetRule().GetEndpoints()[0]
	if ep.GetHost() != "git.example.com" || ep.GetPort() != 443 {
		t.Fatalf("endpoint = %s:%d", ep.GetHost(), ep.GetPort())
	}
	if got := ep.GetCredentialBinding().GetProvider(); got != "p-git" {
		t.Fatalf("provider = %q", got)
	}
}

// TestCredentialPolicyOps_Idempotent: re-applying an already stamped policy
// emits nothing.
func TestCredentialPolicyOps_Idempotent(t *testing.T) {
	policy := imagePolicyDeclaringBoundHosts()
	first, err := CredentialPolicyOps(policy, testGithubBindings())
	if err != nil {
		t.Fatalf("CredentialPolicyOps: %v", err)
	}
	// Fold the emitted rules back into the policy under their ORIGINAL keys
	// (the gateway upserts by rule name and keeps the policy's own key), then
	// merge again.
	// Ops are keyed, so folding them back is a plain keyed assignment — the
	// same upsert the gateway performs.
	for _, op := range first {
		add := op.GetAddRule()
		policy.NetworkPolicies[add.GetRuleName()] = add.GetRule()
	}
	second, err := CredentialPolicyOps(policy, testGithubBindings())
	if err != nil {
		t.Fatalf("CredentialPolicyOps (second): %v", err)
	}
	if len(second) != 0 {
		names := []string{}
		for _, op := range second {
			names = append(names, op.GetAddRule().GetRuleName())
		}
		t.Fatalf("second merge emitted %d ops (%v), want 0 (idempotent)", len(second), names)
	}
}

// TestCredentialPolicyOps_FailClosed: nil policy and conflicting provider both
// refuse instead of producing a partially-bound policy.
func TestCredentialPolicyOps_FailClosed(t *testing.T) {
	if _, err := CredentialPolicyOps(nil, testGithubBindings()); err == nil {
		t.Fatal("nil policy: want error")
	}
	policy := &v1.SandboxPolicy{NetworkPolicies: map[string]*v1.NetworkPolicyRule{
		"github_rest_api": {
			Name: "github-rest-api",
			Endpoints: []*v1.NetworkEndpoint{{
				Host: "api.github.com", Port: 443,
				CredentialBinding: &v1.NetworkCredentialBinding{Provider: "other"},
			}},
		},
	}}
	if _, err := CredentialPolicyOps(policy, testGithubBindings()); err == nil {
		t.Fatal("conflicting provider: want error, not a silent override")
	}
	if ops, err := CredentialPolicyOps(&v1.SandboxPolicy{}, nil); err != nil || ops != nil {
		t.Fatalf("no bindings = (%v, %v), want (nil, nil)", ops, err)
	}
}

func keysOf(m map[string]*v1.NetworkPolicyRule) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
