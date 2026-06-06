# Use private-CIDR exclusion to force MCP traffic through the kapetool sidecar

## Status

accepted

## Context

Handler pods must reach internet LLM APIs on port 443 but must not be able to reach in-cluster MCP servers directly, which would bypass the kapetool sidecar and its allowlist enforcement. All cluster-internal services live in private IP space (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16).

## Decision

On the standard handler-egress policy, the LLM egress `ipBlock` allows `0.0.0.0/0` on 443 but excludes the three private CIDR ranges; the Cilium variant achieves the same via an `egressDeny` to the cluster entity plus explicit FQDN allowances.

## Consequences

Handler pods cannot reach any in-cluster service on 443, so all MCP tool calls must traverse the sidecar on localhost (127.0.0.1), which is not subject to NetworkPolicy. This is load-bearing security, not cosmetic: removing the exclusions silently reopens the sidecar-bypass path.

## Source

- [2026-05-17-phase8-issue-77-network-policy-design.md](../../superpowers/specs/2026-05-17-phase8-issue-77-network-policy-design.md)
