package main

import (
	"context"
	"fmt"
	"log"

	v1 "github.com/dzungtr/kape/prototype/m1-controller/gen"
	
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
