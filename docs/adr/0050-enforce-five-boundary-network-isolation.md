# Enforce the full 5-boundary network isolation model

## Status

accepted

## Context

Without network isolation, any compromised pod can reach MCP servers, NATS, and the audit database directly, bypassing the kapetool sidecar, the `allowedTools` allowlist, and the rest of the security stack. The original iteration scope described only the handler-egress boundary, which leaves MCP servers, postgres, and task-service reachable from arbitrary cluster pods.

## Decision

Ship NetworkPolicy manifests enforcing all five boundaries from spec 0007 section 2: handler-pod egress, kapetool sidecar egress, MCP-server ingress, task-service ingress, and postgres ingress.

## Consequences

The cluster perimeter is fully closed: each component only accepts traffic from its legitimate callers. This requires engineers to label every relevant pod (handler, nats, task-service, dashboard, postgres, mcp-server) correctly, since the policies are label-driven and partial labeling silently breaks connectivity.

## Source

- [2026-05-17-phase8-issue-77-network-policy-design.md](../../superpowers/specs/2026-05-17-phase8-issue-77-network-policy-design.md)
