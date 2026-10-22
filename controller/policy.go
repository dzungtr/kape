package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strconv"

	v1 "github.com/dzungtr/kape/controller/gen"
)

// ModelGatewayRuleName is the stable name of the model-gateway egress rule. The
// credential-binding merge (#264) proves its addition additive against exactly
// this rule, so the name is part of the policy contract.
const ModelGatewayRuleName = "m1_model_gateway"

// modelGatewayRule is the m1 model-gateway egress allowance, derived from the
// configured gateway URL (KAPE_MODEL_GATEWAY_URL): the sandbox may reach
// exactly that endpoint, REST, all methods, as binaries curl + pi/node.
// Without it the sandbox OPA policy proxy denies egress.
//
// The endpoint is never compiled in: patching models.json at one gateway while
// the policy allowlists another would leave the sandbox fail-closed against its
// own model traffic. A malformed URL is an error, not a silent fallback.
func modelGatewayRule(endpoint string) (*v1.NetworkPolicyRule, error) {
	host, port, err := endpointHostPort(endpoint)
	if err != nil {
		return nil, err
	}
	return &v1.NetworkPolicyRule{
		Name: ModelGatewayRuleName,
		Endpoints: []*v1.NetworkEndpoint{{
			Host:     host,
			Port:     port,
			Protocol: "rest",
			Rules: []*v1.L7Rule{{
				Allow: &v1.L7Allow{Method: "*", Path: "**"},
			}},
		}},
		Binaries: []*v1.NetworkBinary{
			{Path: "/usr/bin/pi"},
			{Path: "/usr/bin/curl"},
			{Path: "/usr/bin/node"},
		},
	}, nil
}

// endpointHostPort splits an http(s) base URL into the policy endpoint's host
// and port. An explicit port wins; otherwise the scheme default applies (443
// for https, 80 for http).
func endpointHostPort(endpoint string) (string, uint32, error) {
	if endpoint == "" {
		return "", 0, fmt.Errorf("model-gateway URL is empty")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", 0, fmt.Errorf("parsing model-gateway URL %q: %w", endpoint, err)
	}
	host := u.Hostname()
	if host == "" {
		return "", 0, fmt.Errorf("model-gateway URL %q has no host", endpoint)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return "", 0, fmt.Errorf("model-gateway URL %q must be http or https", endpoint)
	}
	if raw := u.Port(); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, fmt.Errorf("model-gateway URL %q has an invalid port %q", endpoint, raw)
		}
		return host, uint32(n), nil
	}
	if u.Scheme == "https" {
		return host, 443, nil
	}
	return host, 80, nil
}

// ApplyModelGatewayPolicy merges a network rule allowing the sandbox to reach
// the in-cluster model gateway, plus — when the controller carries an env-fed
// binding table (#264, ADR-0088) — the credential-binding rule. Both ride one
// UpdateConfig call: a binding the gateway rejects fails the whole apply, so
// the sandbox never runs with a policy that silently lost its model-gateway
// egress or its promised credentials (fail closed).
func (g *Gateway) ApplyModelGatewayPolicy(ctx context.Context, sandboxName string) error {
	rule, err := modelGatewayRule(g.modelGatewayURL)
	if err != nil {
		// Fail closed before anything reaches the gateway: a policy that cannot
		// be materialised must not leave a sandbox egressing without it.
		return fmt.Errorf("model-gateway egress rule: %w", err)
	}
	ops := []*v1.PolicyMergeOperation{{
		Operation: &v1.PolicyMergeOperation_AddRule{
			AddRule: &v1.AddNetworkRule{RuleName: rule.GetName(), Rule: rule},
		},
	}}
	if len(g.credentialBindings) > 0 {
		bindingRule, err := MergePolicy(&v1.NetworkPolicyRule{Name: CredentialBindingRuleName}, g.credentialBindings)
		if err != nil {
			// Fail closed before anything reaches the gateway: a binding that
			// cannot be materialised must not yield a sandbox that egresses
			// without the credentials it was promised.
			return fmt.Errorf("credential binding policy: %w", err)
		}
		ops = append(ops, &v1.PolicyMergeOperation{
			Operation: &v1.PolicyMergeOperation_AddRule{
				AddRule: &v1.AddNetworkRule{RuleName: bindingRule.GetName(), Rule: bindingRule},
			},
		})
	}
	req := &v1.UpdateConfigRequest{
		WorkspaceScope:  g.scope(),
		Sandbox:         sandboxName,
		MergeOperations: ops,
	}
	if _, err := g.client.UpdateConfig(ctx, req); err != nil {
		return fmt.Errorf("UpdateConfig(policy merge): %w", err)
	}
	log.Printf("[policy] model-gateway rule submitted for %s", sandboxName)
	if len(g.credentialBindings) > 0 {
		log.Printf("[policy] credential-binding rule submitted for %s (%d bindings)", sandboxName, len(g.credentialBindings))
	}
	return nil
}

// ApplyControllerEgressDeny merges an explicit L7 deny for the controller's
// in-cluster service (issue #214, ADR-0083 D2/D7): the daemon is passive and
// has no listener path back to the controller, and the sandbox never carries
// controller credentials — the policy makes that structural. The gateway
// validates deny targets against endpoints already present in the policy
// (verified live on microk8s, issue #217), so the controller endpoint is
// added as a broad allow first and the deny then overrides it: deny rules are
// evaluated after allows and take precedence, so the deny survives future
// broad allow rules; the default-deny OPA posture stays the real boundary.
func (g *Gateway) ApplyControllerEgressDeny(ctx context.Context, sandboxName, host string, port int) error {
	req := &v1.UpdateConfigRequest{
		WorkspaceScope: g.scope(),
		Sandbox:        sandboxName,
		MergeOperations: []*v1.PolicyMergeOperation{{
			Operation: &v1.PolicyMergeOperation_AddRule{
				AddRule: &v1.AddNetworkRule{
					RuleName: "m1_controller",
					Rule: &v1.NetworkPolicyRule{
						Name: "m1_controller",
						Endpoints: []*v1.NetworkEndpoint{{
							Host:     host,
							Port:     uint32(port),
							Protocol: "rest",
							Rules: []*v1.L7Rule{{
								Allow: &v1.L7Allow{Method: "*", Path: "**"},
							}},
						}},
					},
				},
			},
		}, {
			Operation: &v1.PolicyMergeOperation_AddDenyRules{
				AddDenyRules: &v1.AddDenyRules{
					// v0.1.0 types the deny target explicitly: the same rule,
					// host, port set and binary scope the allow endpoint above
					// declared (an empty rule binary list means any binary).
					Target: &v1.L7RuleTarget{
						RuleName:  "m1_controller",
						Host:      host,
						Ports:     []uint32{uint32(port)},
						AnyBinary: true,
					},
					DenyRules: []*v1.L7DenyRule{{
						Method: "*",
						Path:   "**",
					}},
				},
			},
		}},
	}
	if _, err := g.client.UpdateConfig(ctx, req); err != nil {
		return fmt.Errorf("UpdateConfig(deny merge): %w", err)
	}
	log.Printf("[policy] controller egress deny (%s:%d) submitted for %s", host, port, sandboxName)
	return nil
}
