package main

import (
	"context"
	"fmt"
	"log"

	v1 "github.com/dzungtr/kape/controller/gen"
)

// ApplyModelGatewayPolicy merges a network rule allowing the sandbox to reach
// the in-cluster model gateway (host:80, REST, all methods) as binaries
// curl + pi/node. Without this, the sandbox OPA policy proxy denies egress.
func (g *Gateway) ApplyModelGatewayPolicy(ctx context.Context, sandboxName string) error {
	endpoint := &v1.NetworkEndpoint{
		Host:     "model-gateway-http.aperture.svc.cluster.local",
		Port:     80,
		Protocol: "rest",
		Rules: []*v1.L7Rule{{
			Allow: &v1.L7Allow{Method: "*", Path: "**"},
		}},
	}
	rule := &v1.NetworkPolicyRule{
		Name:      "m1_model_gateway",
		Endpoints: []*v1.NetworkEndpoint{endpoint},
		Binaries: []*v1.NetworkBinary{
			{Path: "/usr/bin/pi"},
			{Path: "/usr/bin/curl"},
			{Path: "/usr/bin/node"},
		},
	}
	req := &v1.UpdateConfigRequest{
		Name: sandboxName,
		MergeOperations: []*v1.PolicyMergeOperation{{
			Operation: &v1.PolicyMergeOperation_AddRule{
				AddRule: &v1.AddNetworkRule{RuleName: "m1_model_gateway", Rule: rule},
			},
		}},
	}
	_, err := g.client.UpdateConfig(ctx, req)
	if err != nil {
		return fmt.Errorf("UpdateConfig(policy merge): %w", err)
	}
	log.Printf("[policy] model-gateway rule submitted for %s", sandboxName)
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
		Name: sandboxName,
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
					Host: host,
					Port: uint32(port),
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
