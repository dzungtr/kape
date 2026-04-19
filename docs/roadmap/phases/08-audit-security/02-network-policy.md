# Phase 8.2 — Network Policy

**Status:** pending
**Phase:** 08-audit-security
**Milestone:** M4
**Specs:** 0007

## Goal

Restrict handler pod egress to only required destinations. All other egress denied. Ship standard K8s NetworkPolicy and a Cilium NetworkPolicy variant.

## Work

- `examples/networkpolicy/handler-egress.yaml`: standard K8s `NetworkPolicy`
  - Allow egress to: NATS port 4222, LLM provider port 443, MCP sidecars (by pod label `kape.io/role: mcp`), task-service port 8080, Qdrant port 6333
  - Default deny all other egress
- `examples/networkpolicy/handler-egress-cilium.yaml`: `CiliumNetworkPolicy` equivalent with FQDN-based LLM egress rule (e.g. `api.openai.com`)
- Add comments in each file explaining each rule's purpose

## Acceptance Criteria

- Apply policy to handler pod namespace → `curl 8.8.8.8` from handler pod fails
- `curl nats-svc:4222` from handler pod succeeds
- `curl task-service:8080` from handler pod succeeds

## Key Files

- `examples/networkpolicy/handler-egress.yaml` (new)
- `examples/networkpolicy/handler-egress-cilium.yaml` (new)
