package main

import (
	"context"
	"testing"
)

// The model-gateway egress rule has no compiled-in endpoint: it is derived from
// the configured KAPE_MODEL_GATEWAY_URL, so a deployment that moves its gateway
// moves the allowlist with it. These tests are the executable statement of that
// contract — a hardcoded host would make every case below identical.

func TestModelGatewayRule_DerivesEndpointFromURL(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wantHost string
		wantPort uint32
	}{
		{"http with explicit port", "http://gw.kape-system.svc.cluster.local:8080/v1", "gw.kape-system.svc.cluster.local", 8080},
		{"http default port", "http://gw.kape-system.svc.cluster.local/v1", "gw.kape-system.svc.cluster.local", 80},
		{"https default port", "https://gateway.example.com/v1", "gateway.example.com", 443},
		{"https with explicit port", "https://gateway.example.com:8443/openai/v1", "gateway.example.com", 8443},
		{"bare host with trailing slash", "http://model-gateway:4000/", "model-gateway", 4000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule, err := modelGatewayRule(tc.endpoint)
			if err != nil {
				t.Fatalf("modelGatewayRule(%q): %v", tc.endpoint, err)
			}
			if rule.GetName() != ModelGatewayRuleName {
				t.Fatalf("rule name = %q, want %q", rule.GetName(), ModelGatewayRuleName)
			}
			if len(rule.GetEndpoints()) != 1 {
				t.Fatalf("endpoints = %d, want exactly the configured gateway", len(rule.GetEndpoints()))
			}
			ep := rule.GetEndpoints()[0]
			if ep.GetHost() != tc.wantHost || ep.GetPort() != tc.wantPort {
				t.Fatalf("endpoint = %s:%d, want %s:%d", ep.GetHost(), ep.GetPort(), tc.wantHost, tc.wantPort)
			}
			if ep.GetProtocol() != "rest" {
				t.Fatalf("protocol = %q, want rest (L7 allow path **)", ep.GetProtocol())
			}
			if allow := ep.GetRules()[0].GetAllow(); allow.GetMethod() != "*" || allow.GetPath() != "**" {
				t.Fatalf("L7 allow = %v, want method * path **", allow)
			}
			for _, path := range []string{"/usr/bin/pi", "/usr/bin/curl", "/usr/bin/node"} {
				found := false
				for _, b := range rule.GetBinaries() {
					if b.GetPath() == path {
						found = true
					}
				}
				if !found {
					t.Fatalf("binary %s missing from the allowlist: %v", path, rule.GetBinaries())
				}
			}
		})
	}
}

func TestModelGatewayRule_RejectsUnusableEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{"empty", ""},
		{"no host", "http:///v1"},
		{"non-http scheme", "grpc://gateway:443"},
		{"port out of range", "http://gateway:70000/v1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if rule, err := modelGatewayRule(tc.endpoint); err == nil {
				t.Fatalf("modelGatewayRule(%q) = %v, want an error (no silent fallback)", tc.endpoint, rule)
			}
		})
	}
}

// TestApplyModelGatewayPolicy_UsesConfiguredEndpoint: the merge sent to the
// gateway allows the configured endpoint, not a compiled-in one.
func TestApplyModelGatewayPolicy_UsesConfiguredEndpoint(t *testing.T) {
	client := &stubPolicyClient{}
	gw := newPolicyTestGateway(client)

	if err := gw.ApplyModelGatewayPolicy(context.Background(), "sbx-1"); err != nil {
		t.Fatalf("ApplyModelGatewayPolicy: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("UpdateConfig calls = %d, want 1", len(client.requests))
	}
	ep := client.requests[0].GetMergeOperations()[0].GetAddRule().GetRule().GetEndpoints()[0]
	if ep.GetHost() != "gw.kape-system.svc.cluster.local" || ep.GetPort() != 80 {
		t.Fatalf("policy endpoint = %s:%d, want the configured gateway", ep.GetHost(), ep.GetPort())
	}
}

// TestApplyModelGatewayPolicy_FailClosedOnUnusableURL: an unusable gateway URL
// aborts before the gateway is called — the sandbox must never end up with a
// policy that silently dropped its model-gateway egress.
func TestApplyModelGatewayPolicy_FailClosedOnUnusableURL(t *testing.T) {
	client := &stubPolicyClient{}
	gw := &Gateway{client: client, modelGatewayURL: "not-a-url"}

	err := gw.ApplyModelGatewayPolicy(context.Background(), "sbx-1")
	if err == nil {
		t.Fatal("ApplyModelGatewayPolicy succeeded with an unusable gateway URL, want error")
	}
	if !contains(err.Error(), "model-gateway egress rule") {
		t.Fatalf("error = %q, want it to name the model-gateway egress rule", err)
	}
	if len(client.requests) != 0 {
		t.Fatalf("UpdateConfig called %d times, want 0 (fail closed before any merge)", len(client.requests))
	}
}
