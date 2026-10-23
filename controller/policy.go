package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"time"

	v1 "github.com/dzungtr/kape/controller/gen"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Policy merge concurrency (kape#274): the merge is a read-modify-write against
// the sandbox resource version, so a concurrent composition or revocation wins
// the race as ABORTED and is retried. Bounded on purpose — a merge that cannot
// converge inside the window is a failure the caller must see.
const (
	policyMergeAttempts = 4
	policyMergeBackoff  = 500 * time.Millisecond
)

// validateBindings checks the binding table BEFORE any UpdateConfig is sent
// (fail closed, ADR-0088): a malformed binding must never leave a sandbox
// half-provisioned with its model-gateway rule applied and its credentials
// unbound. ParseBindings already refuses malformed input at startup; this
// catches the runtime edge (e.g. a binding whose hosts were edited after parse).
func validateBindings(bindings []Binding) error {
	for _, binding := range bindings {
		for _, spec := range binding.Hosts {
			if _, _, err := splitHostSpec(spec); err != nil {
				return fmt.Errorf("credential binding %q: %w", binding.Provider, err)
			}
		}
	}
	return nil
}

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

// GetSandboxPolicy reads the sandbox's current effective policy together with
// the sandbox resource version it was read at. It backs the credential stamping
// path (kape#274): the controller must know which rules already declare a bound
// endpoint before it merges credential metadata in, and must pin the write to
// the version it read.
func (g *Gateway) GetSandboxPolicy(ctx context.Context, sandboxName string) (*v1.SandboxPolicy, uint64, error) {
	resp, err := g.client.GetSandboxConfig(ctx, &v1.GetSandboxConfigRequest{
		WorkspaceScope: g.scope(),
		Name:           sandboxName,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("GetSandboxConfig(%s): %w", sandboxName, err)
	}
	// The concurrency token the gateway validates is the SANDBOX resource
	// version (ObjectMeta.resource_version), not the policy version: it moves
	// on any sandbox-scoped mutation, including provider composition.
	sandbox, err := g.client.GetSandbox(ctx, &v1.GetSandboxRequest{WorkspaceScope: g.scope(), Name: sandboxName})
	if err != nil {
		return nil, 0, fmt.Errorf("GetSandbox(%s): %w", sandboxName, err)
	}
	return resp.GetPolicy(), sandbox.GetSandbox().GetMetadata().GetResourceVersion(), nil
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
	// Read-modify-write under the sandbox resource version (kape#274). The
	// gateway composes provider rules into the policy asynchronously after
	// create, and an unguarded merge raced that composition: the request was
	// accepted, then recomposed over, and the credential metadata never reached
	// the enforced policy — silently, since the call succeeded.
	if err := validateBindings(g.credentialBindings); err != nil {
		return fmt.Errorf("credential binding policy: %w", err)
	}
	mergeOps := ops
	var expected uint64
	if len(g.credentialBindings) > 0 {
		// Bindings are stamped into the sandbox's CURRENT policy: an endpoint the
		// policy already declares must be re-sent with credential_binding, not
		// appended as a second declaration of the same host:port (the gateway
		// rejects that overlap as ambiguous credential metadata).
		policy, readVersion, err := g.GetSandboxPolicy(ctx, sandboxName)
		if err != nil {
			// Fail closed: a policy we cannot read is a policy we cannot bind
			// safely, and the sandbox must not run with credentials it may owe.
			return fmt.Errorf("credential binding policy (read sandbox policy): %w", err)
		}
		// A sandbox created moments ago reports an EMPTY policy: the gateway
		// backfills spec.policy from the image on the first UpdateConfig
		// ("backfilled spec.policy from sandbox-discovered policy"). Stamping
		// against that empty read silently takes the additive path, whose rule
		// then collides with the image's own endpoints — the exact ambiguity
		// this stamping exists to avoid. Spend one merge to trigger the
		// backfill, then re-read.
		if len(policy.GetNetworkPolicies()) == 0 {
			if err := g.mergeOrRetry(ctx, sandboxName, ops, readVersion); err != nil {
				return err
			}
			policy, readVersion, err = g.GetSandboxPolicy(ctx, sandboxName)
			if err != nil {
				return fmt.Errorf("credential binding policy (re-read after backfill): %w", err)
			}
			if len(policy.GetNetworkPolicies()) == 0 {
				return fmt.Errorf("credential binding policy: %s still reports no policy after backfill; refusing to add a second declaration of bound endpoints", sandboxName)
			}
			// The model-gateway rule is already applied; only the bindings remain.
			mergeOps = nil
		}
		bindingOps, err := CredentialPolicyOps(policy, g.credentialBindings)
		if err != nil {
			// Fail closed before anything reaches the gateway: a binding that
			// cannot be materialised must not yield a sandbox that egresses
			// without the credentials it was promised.
			return fmt.Errorf("credential binding policy: %w", err)
		}
		mergeOps = append(mergeOps, bindingOps...)
		expected = readVersion
	}
	if len(mergeOps) > 0 {
		if err := g.mergeOrRetry(ctx, sandboxName, mergeOps, expected); err != nil {
			return err
		}
	}
	log.Printf("[policy] model-gateway rule submitted for %s", sandboxName)
	if len(g.credentialBindings) > 0 {
		log.Printf("[policy] credential-binding rule submitted for %s (%d bindings)", sandboxName, len(g.credentialBindings))
	}
	return nil
}

// mergeOrRetry applies one policy merge, retrying ABORTED (the sandbox moved
// under us: provider composition, a revocation, a concurrent merge) a bounded
// number of times. Anything else surfaces immediately — a silent discard is
// what made the credential stamping invisible for a whole debugging session.
func (g *Gateway) mergeOrRetry(ctx context.Context, sandboxName string, ops []*v1.PolicyMergeOperation, expected uint64) error {
	var lastErr error
	for attempt := 0; attempt < policyMergeAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("policy merge: %w", ctx.Err())
			case <-time.After(policyMergeBackoff * time.Duration(attempt)):
			}
		}
		req := &v1.UpdateConfigRequest{
			WorkspaceScope:          g.scope(),
			Sandbox:                 sandboxName,
			MergeOperations:         ops,
			ExpectedResourceVersion: expected,
		}
		if _, err := g.client.UpdateConfig(ctx, req); err != nil {
			if status.Code(err) == codes.Aborted && attempt < policyMergeAttempts-1 {
				lastErr = err
				continue
			}
			return fmt.Errorf("UpdateConfig(policy merge): %w", err)
		}
		return nil
	}
	return fmt.Errorf("UpdateConfig(policy merge) after %d attempts: %w", policyMergeAttempts, lastErr)
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
