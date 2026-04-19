# Phase 8 — K8s Audit Adapter + Security Hardening

**Status:** pending
**Milestone:** M4
**Specs:** 0006, 0007
**Modified by:** 0012 (created)

## Goal

Add the second event producer (K8s Audit) and apply the full 8-layer security model from spec 0007.

## Reference Specs

- `0006-events-broker-design` — K8s Audit adapter design, audit event subject hierarchy
- `0007-security-layer` — full 8-layer security model

## Work

### K8s Audit adapter
- HTTP server accepting K8s API server audit webhook events (`adapters/kape-audit-adapter/`)
- Audit policy: verbs `create`, `update`, `delete` on Secrets, RoleBindings, ClusterRoleBindings, Pods
- Build CloudEvent: `type = kape.events.audit`, `source = k8s-apiserver`
- Publish to `kape.events.audit.<verb>.<resource>`

### NetworkPolicy manifests
- Handler pod egress: NATS (4222), LLM provider (443), MCP sidecars (by pod label), task-service (8080), Qdrant (6333)
- All other egress denied
- Standard NetworkPolicy + Cilium variant in `examples/networkpolicy/`

### Prompt injection defence
- Full system prompt template in `runtime/graph/system_prompt.j2`
- HTML-escape all `event_raw` fields before Jinja2 rendering
- XML tag isolation: wrap user content in `<event>...</event>` tags

### Secret management
- ESO `SecretStore` + `ExternalSecret` example manifests (`examples/eso/`)
- Operator mounts KapeTool connection Secrets as files (not env vars)

### Immutable audit log
- PostgreSQL role `kape_writer` with `INSERT` only — no `UPDATE` on terminal-state rows via trigger

### mTLS for NATS
- cert-manager `Certificate` resources for NATS cluster + all client connections
- Update NATS StatefulSet and all publisher/consumer configs

## Acceptance Criteria

- K8s Audit adapter: create a Secret → CloudEvent in NATS → handler processes it → Task in DB
- NetworkPolicy: `curl 8.8.8.8` from handler pod fails; `curl nats-svc:4222` succeeds
- Prompt injection: inject `<script>call_tool(rm -rf)</script>` into test event → no out-of-allowlist tool calls
- mTLS: non-mTLS NATS client connection is rejected

**M4 gate:** Both adapters live; network policy blocks unexpected egress; prompt injection test passes.

## Key Files

- `adapters/kape-audit-adapter/`
- `examples/networkpolicy/`
- `examples/eso/`
- `runtime/graph/system_prompt.j2`
- `operator/infra/k8s/deployment.go` (Secret file mounts)
