# Phase 6 — Full Operator

**Status:** pending
**Milestone:** M2
**Specs:** 0002, 0005, 0013
**Modified by:** 0012 (created), 0013 (KapeSkillReconciler, KapeProxyReconciler, kapeproxy sidecar model added)

## Goal

The operator manages the full resource lifecycle — KapeTool (memory + mcp types), KapeSchema, KapeSkill, KapeProxy sidecar injection, and KEDA autoscaling. After this phase, a KapeHandler with tools and skills configured deploys completely from a CRD apply.

## Reference Specs

- `0002-crds-design` — KapeTool and KapeSchema field reference
- `0005-kape-operator` — KapeTool reconciler, KapeSchema reconciler, KEDA ScaledObject generation
- `0013-kape-skill-crd` — KapeSkillReconciler, KapeHandlerReconciler changes, kapeproxy sidecar model, kapeproxy-config rendering

## Work

### KapeTool reconciler — memory type
- Provision Qdrant StatefulSet + Service in `kape-system`
- Create Qdrant collection via Qdrant HTTP API
- Create connection Secret (`QDRANT_URL`, `QDRANT_COLLECTION`)
- Inject Secret env vars into all referencing handler Deployments
- On delete: confirm no handlers reference it; delete collection + StatefulSet + Secret

### KapeSkillReconciler (new — from 0013)
- Validate `spec.instruction` and `spec.description` are non-empty
- For each tool in `spec.tools[]`: check KapeTool exists and is Ready
- Set `status.conditions[Ready]`
- Manage finalizer `kape.io/skill-protection`: block deletion while any KapeHandler references the skill
- Kubernetes events: `SkillValid` (Normal), `DeletionBlocked` (Warning)

### KapeHandlerReconciler changes (from 0013)

**Dependency gate extension:**
- foreach skill in `spec.skills[]`:
  - KapeSkill exists → else Pending, reason: `KapeSkillNotFound`
  - KapeSkill.status.conditions[Ready]=True → else Pending, reason: `KapeSkillNotReady`

**Tool union computation:**
```go
toolMap := map[string]KapeTool{}
for _, ref := range handler.Spec.Tools {
    tool := fetchKapeTool(ref.Ref)
    toolMap[tool.Name] = tool
}
for _, skillRef := range handler.Spec.Skills {
    skill := fetchKapeSkill(skillRef.Ref)
    for _, ref := range skill.Spec.Tools {
        tool := fetchKapeTool(ref.Ref)
        toolMap[tool.Name] = tool
    }
}
```

**System prompt assembly:**
- Handler systemPrompt → eager skill instructions (lazyLoad: false, declaration order) → lazy skill preamble
- Lazy skill preamble lists name + description of all lazyLoad: true skills

**Lazy skill ConfigMap:**
- `kape-skills-{handler-name}`: one file per lazy skill (`{skill-name}.txt` with raw instruction)
- Only created if lazy skills exist; mounted at `/etc/kape/skills/` in kapehandler container

**Rollout hash extension:**
```go
rolloutHash = sha256(
    handler.Spec +
    schema.Spec +
    foreach tool in toolMap: tool.Spec +
    foreach skill in handler.Spec.Skills: skill.Spec
)
```

**Label sync extension:**
```
kape.io/skill-ref-{skillname}=true  // one per entry in spec.skills[]
```

### Sidecar injection change (from 0013)
- Replace N `kapetool-*` sidecars with one `kapeproxy` sidecar per handler pod
- Render `kapeproxy-config-{handler-name}` ConfigMap from unified toolMap:
  ```yaml
  upstreams:
    {kapetool-name}:
      url: {KapeTool.spec.mcp.endpoint}
      transport: sse
      allowedTools: [...]
      redaction: {...}
      audit: true
  ```
- Add `kapeproxy.image` and `kapeproxy.version` to `kape-config` ConfigMap

### KapeProxy binary (new — from 0013)
- New Go binary at `cmd/kapeproxy/`
- Startup: read `/etc/kapeproxy/config.yaml`, connect to each upstream, call `tools/list`, filter by `allowedTools`, namespace tools as `{kapetool-name}__{tool-name}`, register in routing table
- Expose single MCP endpoint on `:8080`
- Tool call handling: parse prefix → lookup routing table → apply input redaction → forward to upstream → apply output redaction → emit OTEL span → return response
- Unreachable upstream at startup: log, mark unavailable, continue — do not fail pod startup
- OTEL: W3C TraceContext propagation, child spans under handler root span

### KapeSchema reconciler
- Validate `spec.jsonSchema` is a valid JSON Schema object with `properties`
- Block deletion if any KapeHandler references this schema

### KEDA ScaledObject generation
- Create `ScaledObject` targeting handler Deployment
- `NatsJetStreamScaler` on consumer group lag
- `minReplicas`, `maxReplicas` from `spec.scaling`

### Cross-resource watch
- KapeTool changes trigger KapeHandler reconciliation for all referencing handlers
- KapeSkill changes trigger KapeHandler reconciliation for all referencing handlers

## Acceptance Criteria

- Apply KapeHandler + KapeTool (memory type) → Qdrant StatefulSet appears, handler Deployment has QDRANT_* env vars
- Apply KapeSkill referencing a KapeTool → KapeSkill status shows Ready
- Apply KapeHandler referencing a KapeSkill → handler pod has single `kapeproxy` sidecar (no per-tool sidecars)
- Apply KapeHandler + KapeSkill (lazyLoad: true) → `kape-skills-{name}` ConfigMap exists, mounted at `/etc/kape/skills/`
- Attempt to delete a KapeSkill referenced by a KapeHandler → deletion blocked
- KEDA ScaledObject visible with correct min/max replicas
- kapeproxy `tools/list` returns namespaced tool names (`kapetool-name__tool-name`)

**M2 gate:** Full lifecycle from CRD apply to running handler with Qdrant, KapeSkill, KapeProxy, and KEDA.

## Key Files

- `operator/controller/tool.go`
- `operator/controller/schema.go`
- `operator/controller/skill.go` (new)
- `operator/reconcile/tool.go`
- `operator/reconcile/schema.go`
- `operator/reconcile/skill.go` (new)
- `operator/reconcile/handler.go` (updated: skill gate, tool union, system prompt assembly, kapeproxy config)
- `operator/infra/k8s/kapeproxy.go` (new — sidecar injection)
- `operator/infra/k8s/kapeproxy_config.go` (new — config rendering)
- `operator/infra/qdrant/`
- `operator/infra/k8s/scaledobject.go`
- `cmd/kapeproxy/main.go` (new Go binary)
- `internal/proxy/server.go` (new)
- `internal/proxy/router.go` (new)
