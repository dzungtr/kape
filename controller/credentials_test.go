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
	gatewayRule := modelGatewayRule()
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
		if ep.GetHost() == "model-gateway-http.aperture.svc.cluster.local" {
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
	merged, err := MergePolicy(modelGatewayRule(), []Binding{{Provider: "p-git", Hosts: []string{"github.com:443"}}})
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
	original := modelGatewayRule()
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
	gw := (&Gateway{client: client}).WithCredentialBindings([]Binding{{
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
	if !proto.Equal(gatewayAdd.GetRule(), modelGatewayRule()) {
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

// TestApplyModelGatewayPolicy_NoBindingsKeepsModelGatewayOnly: an empty table
// keeps the pre-#264 request shape exactly.
func TestApplyModelGatewayPolicy_NoBindingsKeepsModelGatewayOnly(t *testing.T) {
	client := &stubPolicyClient{}
	gw := &Gateway{client: client}
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
	gw := (&Gateway{client: client}).WithCredentialBindings([]Binding{{Provider: "p-git", Hosts: []string{"github.com"}}})

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
	gw := (&Gateway{client: client}).WithCredentialBindings([]Binding{{
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
