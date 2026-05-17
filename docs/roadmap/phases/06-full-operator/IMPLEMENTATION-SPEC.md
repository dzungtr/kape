# Phase 6 — Full Operator: Implementation Spec

**Status:** Approved (design session 2026-05-09)
**Milestone:** M2
**Author:** Dzung Tran (designer team-member, kape-io-team)
**Depends on:** specs `0002-crds-design`, `0005-kape-operator`, `0013-kape-skill-crd`
**Phase doc:** [./README.md](./README.md)
**Deferred follow-ups:** [#37](https://github.com/dzungtr/kape/issues/37) (webhook admission), [#38](https://github.com/dzungtr/kape/issues/38) (memory-tool deletion protection + Qdrant collection lifecycle)

---

## 0. Purpose & Scope

This document is the implementation spec for Phase 6 — Full Operator. It decomposes the remaining work (after PR #7) into seven independently-mergeable PR slices, fixes the cross-slice contracts so each slice can be reviewed in isolation, and records the architectural decisions made during the 2026-05-09 design session.

It does NOT replace the Phase 6 [README](./README.md), which remains the source-of-truth for high-level slice scope and acceptance criteria. This spec adds: file-level scope per slice, cross-slice interface contracts, testing strategy, decision log, and risk register.

### What's already shipped (PR #7)

- `KapeToolReconciler`, `KapeSchemaReconciler`, `KapeHandlerReconciler` (12-step reconcile loop)
- Qdrant StatefulSet + Service provisioning for memory-type KapeTools
- KEDA `ScaledObject` generation
- Per-tool `kapetool-*` sidecar injection (one sidecar per `mcp`-type KapeTool)
- Cross-resource watch wiring for KapeTool→Handler and KapeSchema→Handler

### What Phase 6 ships (this spec covers)

Seven PR slices, in this exact order:

1. KapeSkill CRD types + KapeSkillReconciler
2. KapeSchema reconciler audit + gap-fill
3. KapeHandlerReconciler skill gate + tool union + system prompt assembly + lazy-skill ConfigMap
4. Cross-resource watch wiring for KapeSkill
5. kapeproxy-config rendering + kapeproxy sidecar injection (with stub image)
6. KapeProxyReconciler (observability-only)
7. kapeproxy real binary

### What Phase 6 explicitly does NOT ship

- Webhook admission server — deferred (issue #37)
- KapeTool memory deletion protection finalizer — deferred (issue #38)
- Qdrant HTTP collection lifecycle (create/delete) — deferred (issue #38)
- Handler-runtime `load_skill` built-in tool — separate runtime PR, not Phase 6
- Full kapeproxy OTEL child-span split (`policy_check`, `upstream_mcp_call`) — Phase 7 (M3)
- Kapeproxy `/status` endpoint for richer slice 6 condition signals — Phase 7
- Production load/perf hardening on kapeproxy — Phase 7

---

## 1. Architecture & Slice Contents

Each slice's content is listed file-by-file. Acceptance criteria are inherited verbatim from the Phase 6 README and not duplicated here.

### Slice 1 — KapeSkill CRD types + KapeSkillReconciler

**New files**

- `operator/infra/api/v1alpha1/kapeskill_types.go` — `KapeSkill` / `KapeSkillSpec` / `KapeSkillStatus`, kubebuilder marker validations on `description`, `instruction`, `lazyLoad`, `tools[].ref`. CEL `XValidation` reserved for cross-field invariants only — none required at v1alpha1; ship without CEL rules and revisit if an invariant emerges.
- `operator/controller/reconcile/skill.go` — `SkillReconciler` with `Reconcile`, `validateSkillSpec`, `handleDeletion`, finalizer `kape.io/skill-protection`.
- `operator/controller/skill.go` — controller wiring (`SetupSkillReconciler`).
- `operator/infra/k8s/skill_repo.go` — `SkillRepository` adapter.
- `operator/infra/ports/skill.go` — `SkillRepository` port; extend `HandlerRepository.ListHandlersBySkillRef` for the deletion-protection check.
- `crds/kape.io_kapeskills.yaml` — generated CRD manifest.

**Updated files**

- `operator/infra/api/v1alpha1/zz_generated.deepcopy.go` — regenerate.
- `operator/cmd/main.go` — register `SkillReconciler` alongside the other three.

**Tests**

- `operator/controller/reconcile/skill_test.go` — unit + envtest (see §3.2).

**Out of scope for slice 1**

- Handler integration (slice 3).
- Watches that re-enqueue handlers on KapeSkill changes (slice 4).

**Definition of Done (slice 1)**

Per Phase 6 README acceptance: *"Apply KapeSkill referencing a KapeTool → KapeSkill status shows Ready"* AND *"Attempt to delete a KapeSkill referenced by a KapeHandler → deletion blocked."* Both demonstrated by tests. PR raised, SBOM scans run, Snyk Code scan clean.

---

### Slice 2 — KapeSchema reconciler audit + gap-fill

**Background:** PR #7 already shipped JSON Schema validation, the `kape.io/schema-protection` finalizer, and the deletion-block-when-referenced behavior. This slice is an audit + small additions.

**Updated files**

- `operator/controller/reconcile/schema.go` — emit `DeletionBlocked` (Warning) and `SchemaValid` (Normal) Kubernetes events. Currently only `Conditions` are written; events are not.
- `operator/controller/schema.go` — wire an `EventRecorder` into the reconciler if not already present.

**Tests**

- `operator/controller/reconcile/schema_test.go` — extend to assert events are recorded.

**No new files.**

**Definition of Done (slice 2)**

All existing KapeSchema acceptance tests still pass (no regression). New tests demonstrate `DeletionBlocked` (Warning) event and `SchemaValid` (Normal) event are recorded. PR raised with SBOM scans + Snyk Code scan clean.

---

### Slice 3 — KapeHandlerReconciler skill gate + tool union + system prompt assembly + lazy ConfigMap

**Updated files**

- `operator/controller/reconcile/handler.go`
  - Extend `validateDependencies` to include skill resolution and skill-pulled tool resolution. Returns a `resolvedDependencies` struct (see §2.1).
  - Extend `computeRolloutHash` to include sorted `KapeTool` Specs (already partly there; ensure deterministic) and ordered `KapeSkill` Specs.
  - Extend label sync (Step 9) to write both `kape.io/skill-ref-{name}=true` (one per `spec.skills[]`) and `kape.io/tool-ref-{name}=true` for every tool in the unioned set.
  - Add a step that reconciles the `kape-skills-{handler-name}` ConfigMap when at least one lazy skill exists; deletes it when none exist.
  - Update `Ready` rollup to be forward-compatible with future condition types (rollup logic: `Ready=True` iff no condition is explicitly `False`, instead of enumerating positives).
- `operator/infra/k8s/deployment.go` — mount `/etc/kape/skills` from `kape-skills-{handler-name}` ConfigMap when it exists; do nothing when it does not.
- `operator/infra/toml/renderer.go` — system prompt assembly: handler.systemPrompt → eager skill instructions (declaration order) → lazy skill preamble (name + description). Implementation matches spec 0013 §3.2.

**New files**

- `operator/controller/reconcile/system_prompt.go` — pure function `AssembleSystemPrompt(handler, eagerSkills, lazySkills) string`. Extracted to keep `handler.go` focused.
- `operator/infra/k8s/skill_configmap.go` — `SkillConfigMapAdapter` for the `kape-skills-{handler-name}` resource.
- `operator/infra/ports/skill_configmap.go` — port.

**Tests**

- `operator/controller/reconcile/handler_test.go` — extend with skill-gate, lazy-vs-eager, and hash-stability cases.
- `operator/controller/reconcile/system_prompt_test.go` — unit tests for the pure function.

**Note on `load_skill` runtime tool:** Phase 6 mounts the lazy ConfigMap but does not register a `load_skill` tool in the handler runtime. That is a separate runtime PR. The mount is a forward-compatible affordance; the runtime ignores it today.

**Definition of Done (slice 3)**

Per Phase 6 README acceptance: *"Apply KapeHandler + KapeSkill (lazyLoad: true) → kape-skills-{name} ConfigMap exists, mounted at /etc/kape/skills/."* AND a KapeSkill with not-Ready KapeTool puts the KapeHandler in Pending with reason `KapeSkillNotReady`. AND eager-skill instruction text appears in settings.toml in declaration order. AND `computeRolloutHash` changes when a referenced KapeSkill's `.Spec` content changes. All four demonstrated by tests. PR raised with SBOM scans + Snyk Code scan clean.

---

### Slice 4 — Cross-resource watch wiring for KapeSkill

**Updated files**

- `operator/controller/watches.go` — add `MapSkillToHandlers(c)` function mirroring the existing `MapToolToHandlers`. Uses label selector `kape.io/skill-ref-{name}=true`.
- `operator/controller/handler.go` — add the watch on `KapeSkill` with the new mapper (alongside the existing `KapeTool` and `KapeSchema` watches).

**Tests**

- `operator/controller/watches_test.go` — extend to verify the new mapper enqueues the right handlers.

**Note:** No new label sync. Slice 3 already writes the labels this watch reads.

**Definition of Done (slice 4)**

Editing a KapeSkill's spec triggers a reconcile of every referencing KapeHandler — verified by observing the rollout-hash annotation change on the handler's Deployment within one reconcile cycle. PR raised with SBOM scans + Snyk Code scan clean.

---

### Slice 5 — kapeproxy-config rendering + sidecar injection (with stub image)

**Updated files (operator)**

- `operator/domain/config/config.go` — add `KapeproxyImage`, `KapeproxyImageVersion` fields with defaults `kape/kapeproxy` / `latest`. Add `KapeproxyImageRef()` helper. Update `WithDefaults`.
- `operator/infra/k8s/kapeconfig.go` — read the two new keys (`kapeproxy.image`, `kapeproxy.version`) from the `kape-config` ConfigMap.
- `operator/infra/k8s/deployment.go` — replace per-tool `kapetool-*` sidecar loop with a single `kapeproxy` container. Add `volumeMounts` for `kapeproxy-config` (always) and `kape-skills` (when present). Resource requests/limits: `requests cpu=100m memory=128Mi`, `limits cpu=500m memory=256Mi` per spec 0013 §4.3 (hardcoded; tunability is a future enhancement).
- `operator/controller/reconcile/handler.go` — add a step (between current Step 5 and Step 6) that renders and ensures the `kapeproxy-config-{handler-name}` ConfigMap. Include `cfg.KapeproxyImage` + `cfg.KapeproxyImageVersion` in `computeRolloutHash` so kape-config bumps trigger handler rollouts.

**New files (operator)**

- `operator/infra/k8s/kapeproxy_config.go` — `KapeproxyConfigAdapter` and the YAML renderer (see §2.2 for the schema).
- `operator/infra/ports/kapeproxy_config.go` — port.

**New files (kapeproxy module — STUB scope only)**

- `kapeproxy/go.mod`, `kapeproxy/go.sum` — new top-level Go module.
- `kapeproxy/cmd/kapeproxy-stub/main.go` — static-config stub. Reads `/etc/kapeproxy/config.yaml`, parses `upstreams.<name>.allowedTools`, returns hardcoded namespaced names (`{kapetool}__{toolname}`) on `tools/list`, returns MCP error `-32603 server not yet available` on `tools/call`. Listens on `:8080`. Comment: `// DEPRECATED: removed in Phase 6 slice 7`.
- `kapeproxy/Dockerfile.stub` — minimal multi-stage build of the stub binary.
- `kapeproxy/README.md` — documents the stub as transitional + slice-7 cleanup expectation.

**CI**

- The slice 5 PR's CI workflow builds and pushes `kape/kapeproxy:stub` (or whatever stable tag matches `kapeproxy.version` in the slice-5 default) to the registry. Without this, slice 5 is not deliverable.

**Tests**

- `operator/infra/k8s/kapeproxy_config_test.go` — golden YAML output for: 1 tool, multiple tools, allowedTools omitted when empty, redaction populated, mcp+memory mix (memory excluded).
- `operator/infra/k8s/deployment_test.go` — assert single kapeproxy container + no `kapetool-*` containers + correct mounts.
- `kapeproxy/cmd/kapeproxy-stub/main_test.go` — table-test the stub's `tools/list` output for the documented config schema. (Lightweight; stub is going away in slice 7.)

**Definition of Done (slice 5)**

Per Phase 6 README acceptance: *"Apply KapeHandler referencing a KapeSkill → handler pod has single kapeproxy sidecar (no per-tool sidecars)"* AND *"kapeproxy `tools/list` returns namespaced tool names (`kapetool-name__tool-name`)"*. The stub binary delivers the second criterion in slice 5; slice 7 carries it forward unchanged. The `kape/kapeproxy:stub` image is built and pushed by slice 5's CI workflow before merge. PR raised with SBOM scans (existing 3 modules) + Snyk Code scan clean.

---

### Slice 6 — KapeProxyReconciler (observability-only)

**New files**

- `operator/controller/reconcile/kapeproxy.go` — `KapeProxyReconciler`. Watches `Pod` resources with label `kape.io/handler=*`; for each pod, examines the kapeproxy container's status and writes `KapeProxyReady` / `KapeProxyDegraded` conditions on the parent `KapeHandler`.
- `operator/controller/kapeproxy.go` — controller wiring.
- `operator/infra/ports/pod.go` — `PodReader` port (read-only Pod status access).
- `operator/infra/k8s/pod_reader.go` — adapter.

**Updated files**

- `operator/cmd/main.go` — register the new controller.

**Tests**

- `operator/controller/reconcile/kapeproxy_test.go` — unit + envtest:
  - Healthy kapeproxy → `KapeProxyReady=True`.
  - CrashLoopBackOff → `KapeProxyReady=False`, `KapeProxyDegraded=True`, reason `RestartLoop`.
  - Pod missing kapeproxy container → `KapeProxyReady=False`, reason `KapeProxyMissing`.

**Explicit non-scope**

- Does NOT write the `kapeproxy-config-{handler-name}` ConfigMap (slice 5 owns it).
- Does NOT mutate `Deployment` resources.
- Does NOT enumerate upstream MCP servers (no /status endpoint exists in slice 5's stub or slice 7's binary at v1; coarse condition is acceptable for M2).

**Definition of Done (slice 6)**

A KapeHandler with a healthy kapeproxy sidecar shows `KapeProxyReady=True` on `kubectl get kapehandler -o yaml`. A handler whose kapeproxy container is in CrashLoopBackOff shows `KapeProxyReady=False` AND `KapeProxyDegraded=True` with reason `RestartLoop`. A handler pod missing the kapeproxy container shows `KapeProxyReady=False` with reason `KapeProxyMissing`. All three demonstrated by tests. PR raised with SBOM scans + Snyk Code scan clean.

---

### Slice 7 — kapeproxy real binary

**New files (kapeproxy module — REAL scope)**

- `kapeproxy/cmd/kapeproxy/main.go` — entrypoint. Reads `/etc/kapeproxy/config.yaml`, builds upstream connections, starts MCP server on `:8080`. Standard Go signal handling for graceful shutdown (SIGTERM → drain → exit).
- `kapeproxy/internal/proxy/server.go` — MCP server endpoint. Inbound W3C TraceContext extraction.
- `kapeproxy/internal/proxy/router.go` — namespaced-tool-name routing table (`{kapetool}__{toolname}` → upstream + original name + redaction + audit flag).
- `kapeproxy/internal/proxy/upstream.go` — per-upstream MCP client (sse / streamable-http via the MCP Go SDK). Outbound TraceContext propagation. Unreachable-at-startup is non-fatal (logs, marks unavailable, returns MCP error on calls to that upstream's tools).
- `kapeproxy/internal/proxy/redaction.go` — JSONPath input/output redaction.
- `kapeproxy/internal/proxy/audit.go` — structured audit log entry per call.
- `kapeproxy/internal/proxy/otel.go` — single `kapeproxy.tool_call` span per call with attributes `tool.namespaced_name`, `tool.upstream`, `tool.original_name`, `tool.allowed`, `tool.latency_ms`, `error`, `kape.task_id`. OTLP exporter wired by env var (`OTEL_EXPORTER_OTLP_ENDPOINT`).
- `kapeproxy/Dockerfile` — production multi-stage build.

**Removed files**

- `kapeproxy/cmd/kapeproxy-stub/main.go`
- `kapeproxy/Dockerfile.stub`
- (Slice 7's PR description must call out removal of the stub artifacts and registry-side cleanup of the `:stub` tag.)

**Updated files**

- `kapeproxy/README.md` — replace transitional language with production usage.
- `helm/` and `examples/` — bump the `kapeproxy.version` reference in any sample `kape-config` ConfigMap to the slice-7 release tag.

**Tests**

- `kapeproxy/internal/proxy/*_test.go` — unit tests for parser, router, redaction, audit, OTEL.
- `kapeproxy/integration_test.go` — in-process mock MCP server; assert allowlist filtering, redaction, OTEL span emission, unreachable-upstream graceful behavior.
- `operator/cmd/playground/` — one envtest scenario exercising real handler + real kapeproxy + mock MCP.

**PR Checklist amendment**

The kape-io PR checklist (in `kape-io/CLAUDE.md`) lists three Go modules for SBOM scans: `./adapters`, `./operator`, `./task-service`. Slice 7's PR must:

1. Run `mcp__Snyk__snyk_sbom_scan` against `./kapeproxy` in addition to the three existing modules.
2. Extend the SBOM PR comment template to include a fourth row (`kapeproxy`).
3. Update `kape-io/CLAUDE.md` to add the new module to the standing PR checklist.

**OTEL profile (M2)**

- Single `kapeproxy.tool_call` span per call. No separate `policy_check` / `upstream_mcp_call` child spans.
- Inbound W3C TraceContext extraction; outbound propagation to upstream MCPs.
- Phase 7 (M3) refines this with the child-span split as part of broader observability hardening.

**Definition of Done (slice 7)**

End-to-end round trip works: real handler pod with real kapeproxy sidecar talking to a mock MCP upstream — `tools/list` returns namespaced+filtered names, `tools/call` for an allowed tool reaches the upstream with input redacted and response output redacted, `tools/call` for a disallowed tool returns MCP error -32601, an unreachable upstream is non-fatal at startup. A single `kapeproxy.tool_call` span is emitted per call with the documented attributes. The stub artifacts (`kapeproxy/cmd/kapeproxy-stub/main.go`, `kapeproxy/Dockerfile.stub`) are removed and the registry-side `:stub` tag is cleaned up. The PR Checklist amendment is applied (`kape-io/CLAUDE.md` includes `./kapeproxy`; SBOM PR comment template has 4 rows). PR raised with all 4 SBOM scans + Snyk Code scan clean.

---

## 2. Cross-Slice Interface Contracts

### 2.1 Unified `toolMap` (slices 3 + 5 contract)

Slice 3 introduces it; slice 5 consumes it.

```go
// operator/controller/reconcile/handler.go (extended in slice 3)
type resolvedDependencies struct {
    Schema  *v1alpha1.KapeSchema
    Tools   []v1alpha1.KapeTool        // toolMap.Values(), sorted by Name
    Skills  []v1alpha1.KapeSkill       // handler.spec.skills[] declaration order
    ToolMap map[string]v1alpha1.KapeTool // keyed by KapeTool.Name; union of handler + skill-pulled
}
```

**Population order (slice 3 implementation):**

1. For each `ref` in `handler.spec.tools[]`: fetch KapeTool; if not Ready, gate Pending. Else `toolMap[tool.Name] = tool`.
2. For each `skillRef` in `handler.spec.skills[]`: fetch KapeSkill; if not Ready, gate Pending.
3. For each tool ref in `skill.spec.tools[]`: fetch KapeTool; if not Ready, gate Pending. Else `toolMap[tool.Name] = tool` (overwrite is a no-op given KapeTool name uniqueness).
4. Sort `Tools` slice by name for hash stability.

**Why keyed by Name (not by ref):** Spec 0013 §3.2 specifies dedup-by-name semantics for skill-pulled tools.

**Hash extension (pseudocode; real implementation uses `json.Marshal` + `h.Write`):**

```
computeRolloutHash(handler, schema, deps, cfg) → sha256 over (in this order):
  1. handler.Spec
  2. schema.Spec
  3. for each tool in deps.Tools (sorted by Name): tool.Spec
  4. for each skill in deps.Skills (declaration order): skill.Spec
  5. cfg.KapeproxyImage          ← slice 5 addition
  6. cfg.KapeproxyImageVersion   ← slice 5 addition
```

Slice 3 implements lines 1-4. Slice 5 adds lines 5-6 when introducing the kape-config kapeproxy fields.

**Skills declaration order (not sorted):** Reordering `handler.spec.skills[]` changes the system prompt assembly order (eager skill text is concatenated in declaration order per spec 0013 §3.2). The hash must therefore reflect order, not just set membership.

### 2.2 kapeproxy-config YAML schema (slice 5 produces, slice 5 stub + slice 7 binary consume)

```yaml
# /etc/kapeproxy/config.yaml
upstreams:
  <kapetool-name>:
    url: <KapeTool.spec.mcp.upstream.url>
    transport: sse | streamable-http
    allowedTools:                    # OMITTED when empty
      - <tool-name>
      - ...
    redaction:                        # OMITTED when no rules
      input:
        - jsonPath: "$.path.to.field"
      output:
        - jsonPath: "$.path.to.field"
    audit: true | false               # default true
```

**Source-of-truth mapping (slice 5 implementation):**

| Config field         | KapeTool source                                        |
| -------------------- | ------------------------------------------------------ |
| `upstreams.<name>`   | `KapeTool.metadata.name` (only `spec.type == "mcp"`)   |
| `.url`               | `KapeTool.spec.mcp.upstream.url`                       |
| `.transport`         | `KapeTool.spec.mcp.upstream.transport`                 |
| `.allowedTools`      | `KapeTool.spec.mcp.allowedTools` (omit field if empty) |
| `.redaction.input`   | `KapeTool.spec.mcp.redaction.input`                    |
| `.redaction.output`  | `KapeTool.spec.mcp.redaction.output`                   |
| `.audit`             | `KapeTool.spec.mcp.audit.enabled` (default `true`)     |

**`memory` and `event-publish` tools are excluded.** Slice 5 must filter on `Type == "mcp"` before rendering. They get env-var injection (Qdrant) or library-level handling (event-publish) instead.

**`allowedTools: []` semantics:** Field omitted entirely → kapeproxy interprets missing field as "no filter, expose all upstream tools." Matches existing kapetool sidecar behavior. Slice 5 stub and slice 7 real binary must both implement this rule.

### 2.3 KapeHandler status condition vocabulary (slices 3, 5, 6 contract)

Single owner per condition type — no two slices write the same one.

| Condition Type           | Owner   | Reasons                                                                                            |
| ------------------------ | ------- | -------------------------------------------------------------------------------------------------- |
| `DependenciesReady`      | slice 3 | `Ready`, `KapeSchemaInvalid`, `KapeToolNotReady`, `KapeSkillNotFound` (new), `KapeSkillNotReady` (new) |
| `ScalingConfigured`      | PR #7   | `ScalingConfigured`, `InvalidScalingConfig`                                                        |
| `DeploymentAvailable`    | PR #7   | `Available`, `MinimumReplicasUnavailable`, `DeploymentNotFound`                                    |
| `Ready`                  | slice 3 | rollup; `Ready=True` iff no condition is explicitly `False`                                        |
| `KapeProxyReady` (new)   | slice 6 | `Ready`, `ContainerCrashLoop`, `ContainerNotReady`, `KapeProxyMissing`                             |
| `KapeProxyDegraded` (new)| slice 6 | `RestartLoop`, `Healthy` (False), (richer signals in Phase 7 with kapeproxy `/status` endpoint)    |

**Forward-compatibility rule:** Slice 3's `Ready` rollup logic must NOT enumerate required-positive conditions. It must use the negative form (`Ready=False` if any condition is `False`; `Ready=True` otherwise). This lets slice 6 add `KapeProxyReady` without slice 3 needing edits.

### 2.4 Reconciler ownership matrix

| Resource                                   | Owner                            | Note                                                  |
| ------------------------------------------ | -------------------------------- | ----------------------------------------------------- |
| `KapeTool` (CRD)                           | KapeToolReconciler               | unchanged from PR #7                                  |
| `KapeSchema` (CRD)                         | KapeSchemaReconciler             | slice 2 adds events only                              |
| `KapeSkill` (CRD)                          | KapeSkillReconciler              | NEW — slice 1                                         |
| `KapeHandler` (CRD)                        | KapeHandlerReconciler            | slice 3 extends                                       |
| `Deployment` (`kape-handler-{name}`)       | KapeHandlerReconciler            | slice 5 reshapes container list                       |
| `ConfigMap` (`kape-handler-{name}`)        | KapeHandlerReconciler            | settings.toml — unchanged                             |
| `ConfigMap` (`kapeproxy-config-{name}`)    | KapeHandlerReconciler            | NEW — slice 5; **NOT slice 6**                        |
| `ConfigMap` (`kape-skills-{name}`)         | KapeHandlerReconciler            | NEW — slice 3; only when lazy skills exist            |
| `ServiceAccount`, `StatefulSet`, `ScaledObject` | KapeHandlerReconciler       | unchanged                                             |
| `Pod` (kapeproxy sidecar status)           | KapeProxyReconciler              | NEW — slice 6; READ-ONLY; writes parent status only   |

---

## 3. Testing Strategy

### 3.1 Tooling baseline

- **envtest harness** at `operator/cmd/playground/` — full controller integration tests against a real apiserver+etcd binary. Already present.
- **Unit tests** alongside Go files (`_test.go`) — repo convention.
- **Snyk SBOM scans** required pre-PR per `kape-io/CLAUDE.md`. Slice 7 extends the standing list to include `./kapeproxy`.
- **Podman compose stack** at `playground/` (top-level) — reachable via Tilt for manual verification.

### 3.2 Per-slice test requirements

**Slice 1 — KapeSkill CRD + KapeSkillReconciler**

| Layer    | Coverage                                                                                                                                                                                                                  |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Unit     | `validateSkillSpec`, finalizer add/remove, condition setter                                                                                                                                                              |
| envtest  | Apply valid KapeSkill + Ready KapeTool → `Ready=True`. Apply with missing KapeTool → `Ready=False`, reason `KapeToolNotFound`. Apply with not-Ready KapeTool → `Ready=False`, reason `KapeToolNotReady`. Delete-while-referenced blocked + `DeletionBlocked` event. Delete-after-handler-removes-ref unblocks. |

**Slice 2 — KapeSchema audit + gap-fill**

| Layer    | Coverage                                                                                  |
| -------- | ----------------------------------------------------------------------------------------- |
| envtest  | All existing schema acceptance tests still pass. `DeletionBlocked` and `SchemaValid` events recorded. |

**Slice 3 — Skill gate, tool union, system prompt assembly, lazy ConfigMap**

| Layer    | Coverage                                                                                                                                          |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| Unit     | `AssembleSystemPrompt` deterministic. `unionToolMap` dedup + sort. `computeRolloutHash` changes on skill reorder + on skill-content change.       |
| envtest  | Eager+lazy mix → settings.toml correct + lazy ConfigMap with right files. Only-eager skills → no lazy ConfigMap. Skill not Ready → handler Pending. |

**Slice 4 — Cross-resource watch wiring**

| Layer    | Coverage                                                                                       |
| -------- | ---------------------------------------------------------------------------------------------- |
| Unit     | `MapSkillToHandlers` mapper enqueue logic                                                      |
| envtest  | KapeSkill spec change → handler reconcile triggered (verified via rollout-hash annotation diff) |

**Slice 5 — kapeproxy-config + sidecar injection (with stub)**

| Layer        | Coverage                                                                                                                                |
| ------------ | --------------------------------------------------------------------------------------------------------------------------------------- |
| Unit         | `renderKapeproxyConfig` golden YAML for: 1 tool, multiple tools, allowedTools omitted when empty, redaction populated, mcp+memory mix.  |
| envtest      | Handler with mcp tools → `kapeproxy-config-{name}` ConfigMap exists, Deployment has 1 kapeproxy container, no `kapetool-*`. Handler with no mcp tools → no `kapeproxy-config`, no kapeproxy container. |
| Stub-image   | One CI smoke test exercises the stub: spin up kind cluster, apply a sample handler, curl `:8080/tools/list`, assert namespaced names.   |

**Slice 6 — KapeProxyReconciler**

| Layer    | Coverage                                                                                                                              |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| Unit     | `evaluatePodHealth(pod)` returns one of `{Ready, Degraded:RestartLoop, Degraded:ContainerNotReady, Missing}`                         |
| envtest  | Healthy → `KapeProxyReady=True`. CrashLoopBackOff → `Ready=False, Degraded=True, RestartLoop`. Missing kapeproxy container → `Missing`. |

**Slice 7 — kapeproxy real binary**

| Layer       | Coverage                                                                                                                                                                                                                |
| ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Unit        | Config parser, router (prefix → upstream), redaction (input + output), audit entry shape, TraceContext extract + inject                                                                                                |
| Integration | In-process mock MCP server: `tools/list` returns namespaced+filtered names; `tools/call` allowed reaches upstream with redacted input; `tools/call` disallowed returns MCP error -32601; unreachable upstream non-fatal. |
| End-to-end  | One envtest scenario: real handler + real kapeproxy + mock MCP, full round-trip                                                                                                                                         |
| OTEL        | Mock OTEL exporter; assert single `kapeproxy.tool_call` span with required attributes; W3C TraceContext extracted + propagated.                                                                                          |
| SBOM        | New `kapeproxy/` module gets its own scan; PR comment template extended with 4th row.                                                                                                                                    |

### 3.3 Test-burden distribution

| Slice | Approx LoC | Notes                                                |
| ----- | ---------- | ---------------------------------------------------- |
| 1     | ~150       | Mostly envtest                                       |
| 2     | ~30        | Event recorder additions only                        |
| 3     | ~250       | Heaviest unit-test layer                             |
| 4     | ~50        | Mapper + one enqueue assertion                       |
| 5     | ~200       | Golden YAML + envtest sidecar shape + stub smoke     |
| 6     | ~120       | Pod health classification + status propagation       |
| 7     | ~400       | Mock upstream + redaction + OTEL                     |

Slice 3 and slice 7 are the meaningful ones; everything else is mechanical. Matches risk distribution.

### 3.4 Acceptance criteria

Each slice's PR description must paste the matching acceptance criterion verbatim from `./README.md` and a test must demonstrate it. No new acceptance criteria are introduced by this implementation spec — the README's are authoritative.

---

## 4. Decision Log

| #   | Decision                                                                                                  | Rationale                                                                                                            |
| --- | --------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| D1  | Slices 1-7 are firm; deliverability solved inside the slice, not by reordering                            | User direction; preserves PR-by-PR reviewability                                                                     |
| D2  | Slice 5 ships a stub kapeproxy image; slice 7 swaps in real binary                                         | Only path that meets slice 5's own acceptance criteria without depending on slice 7                                  |
| D3  | New top-level `kapeproxy/` Go module                                                                       | Mirrors `adapters/`, `task-service/` precedent; isolates MCP SDK as first-class dep                                  |
| D4  | `load_skill` runtime tool out of scope for Phase 6                                                         | Phase 6 README's slice-3 acceptance stops at "ConfigMap mounted"; runtime ships separately                           |
| D5  | Webhook admission deferred → tracked in [#37](https://github.com/dzungtr/kape/issues/37)                   | CRD CEL + reconciler validation cover M2 needs; webhook needs cert mgmt + own phase                                  |
| D6  | Slice 5 stub uses static-config behaviour (not liveness-only)                                              | Lets slice 5 acceptance verify config plumbing end-to-end without slice 7                                            |
| D7  | Handlers labelled with `kape.io/skill-ref-{name}=true` per skill ref                                       | Mirrors existing `kape.io/tool-ref-` pattern                                                                         |
| D8  | Transitive label sync — handler labels include all unioned tools (handler-direct ∪ skill-pulled)           | Existing `MapToolToHandlers` watch keeps working without modification                                                |
| D9  | KapeTool memory deletion: Phase 6 ships nothing additional → tracked in [#38](https://github.com/dzungtr/kape/issues/38) | Today's StatefulSet ownerReference cascade is acceptable for M2; protection finalizer + Qdrant HTTP cleanup deferred |
| D10 | No KEDA gate on tool-unreachable conditions                                                                | Spec 0013 invariant: data-plane must keep ack'ing NATS during partial outages                                        |
| D11 | Slice 7 OTEL: single `kapeproxy.tool_call` span; child-span split deferred to Phase 7                      | Minimum-viable OTEL gives operators enough; full split is speculative observability work                             |
| D12 | Slice 5 renders kapeproxy-config inline in handler reconciler; slice 6 is observability-only               | Single-writer per resource; slice 6 earns its keep via `KapeProxyReady`/`KapeProxyDegraded` conditions               |
| D13 | `toolMap` keyed by `KapeTool.Name`, sorted iteration for hash stability                                    | Spec 0013 §3.2 dedup-by-name semantics; sort ensures deterministic hash                                              |
| D14 | ~~`allowedTools: []` means "expose all" — field omitted from kapeproxy-config when empty~~ **(SUPERSEDED 2026-05-17 by D20 in [2026-05-17-kapeproxy-slice7-fixup spec](../../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md): empty/omitted now means deny-all)** | Matches existing kapetool sidecar behaviour                                                                          |
| D15 | `KapeProxyDegraded` set only on Pod-level signals in slice 6                                               | Log-scraping is brittle; richer kapeproxy `/status` endpoint deferred to Phase 7                                     |
| D16 | `allowedTools` is a deny-by-default glob-pattern list (`path.Match`) intersected with `upstream.ListTools()`; both `tools/list` and `tools/call` agree on the same exposed set | Original D14 only defined the empty case (as "expose all", now superseded); the populated case was filled in wrong by the slice-7 plan. See [2026-05-17-kapeproxy-slice7-fixup spec](../../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md) §3.A |
| D17 | Operator in-code default for `kapeproxy` image version is `latest`; release pins live in `helm/values.yaml` | Spec §2.1 already mandates `latest`; release-coupling does not belong in Go. See [fixup spec](../../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md) §3.B |
| D18 | Playground and Tilt build/reference `kape/kapeproxy:local` (unversioned local-dev tag) | Decouple dev tooling from release versions. See [fixup spec](../../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md) §3.B |
| D19 | The slice-5 stub binary and `.github/workflows/kapeproxy-stub.yml` are both removed in the fixup (completes slice-7 Task 12) | Time-bounding the stub per D2 + R1. See [fixup spec](../../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md) §3.C |
| D20 | **Supersedes D14.** `allowedTools` is deny-by-default — `nil`/omitted/`[]` exposes zero tools; `["*"]` is the explicit opt-in to expose all. Applies uniformly to `tools/list` (omit) and `tools/call` (reject with MCP `-32601`) | Security posture for an audited proxy: operator must opt in, not opt out. Migration: existing KapeTools relying on D14's "omit means expose all" must add `allowedTools: ["*"]` (or, preferred, a minimum-privilege allowlist). See [fixup spec](../../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md) §5/D20 and §7 |

---

## 5. Risks and Mitigations

**R1 — Slice 5 stub becomes load-bearing test infrastructure.**
The stub from D2/D6 may end up referenced by playground configs, dashboard examples, or external runbooks during the slice-5-to-slice-7 gap. Slice 7's "delete the stub" cleanup could break consumers.
*Mitigation:* Slice 5's PR description labels the stub explicitly as transitional and time-bounded; `// DEPRECATED: removed in Phase 6 slice 7` comment in the stub's `main.go`; slice 7's PR description reiterates removal. `kapeproxy/README.md` documents the stub image as not a stable artifact.

**R2 — `load_skill` runtime gap creates a confusing operator experience.**
A user can apply a `lazyLoad: true` KapeSkill in M2; operator mounts the ConfigMap; runtime ignores it. The agent never receives the skill instruction. There's no operator-side error.
*Mitigation:* Until the runtime PR ships, operators set `lazyLoad: false` on all skills. A future runtime PR closes the gap; no operator-side change needed. Document this in the slice 3 PR description and surface in dashboard tooltips when added.

**R3 — Memory KapeTool delete causes silent handler outage.**
Per D9 — Phase 6 ships nothing for it. Issue [#38](https://github.com/dzungtr/kape/issues/38) tracks the gap.
*Mitigation:* Operational pre-flight check: `kubectl get kapehandler -A -l kape.io/tool-ref-{toolname}=true` before deleting a memory KapeTool. Dashboard runbook update recommended. Recovery path: re-apply the KapeTool (StatefulSet recreates against retained PVC), trigger handler rollout.

**R4 — Hash thrash from skill-content edits causing pod rollouts.**
Per D13 — slice 3 includes skill `.Spec` content in the handler rollout hash. Editing a skill's instruction text rolls every referencing handler. Spec 0013 wants this (skill content change should propagate), but for a frequently-edited skill it could cause a lot of restarts.
*Mitigation:* Acceptable behaviour for M2 — explicit and documented. If this becomes a real complaint, a future enhancement could split eager vs lazy skill updates (lazy needs no rollout, just a ConfigMap update).

**R5 — Slice 6's `KapeProxyDegraded` signal is coarse without runtime cooperation.**
Per D15 — Pod-level signals miss "kapeproxy is up but failing all tool calls because every upstream is unreachable." Operators won't see degradation in `kubectl get kapehandler` until something kills the kapeproxy container.
*Mitigation:* Spec calls out that "all upstreams unreachable" is invisible to slice 6. Phase 7 work to add a kapeproxy `/status` endpoint will close this gap. For M2, OTEL traces (D11) are the operator's signal for "tools failing" — not the KapeHandler condition.

**R6 — Snyk SBOM coverage for the new `kapeproxy/` module.**
Slice 7 introduces `./kapeproxy` as a new module; existing PR checklist lists only `./adapters`, `./operator`, `./task-service`.
*Mitigation:* Slice 7 must extend the SBOM PR comment template to a 4th row, and update `kape-io/CLAUDE.md` so future PRs run the scan.

---

## 6. Deferred Implementation Details

These came up during exploration but did not warrant design-session decisions; the slice owner decides at implementation time. Documented here so reviewers know they're intentional.

- **OQ1: kape-config rollout strategy.** When `kapeproxy.image` / `kapeproxy.version` change in `kape-config`, all KapeHandler rollout hashes must change. Slice 5 includes `cfg.KapeproxyImage` + `cfg.KapeproxyImageVersion` in `computeRolloutHash` (see §2.1).
- **OQ2: kapeproxy resource limits.** Hardcoded in M2 per spec 0013 §4.3 (`requests cpu=100m memory=128Mi`, `limits cpu=500m memory=256Mi`). Tunability via `kape-config` is a future enhancement; not blocking for M2.
- **OQ3: kapeproxy graceful shutdown.** Standard Go signal handling (SIGTERM → drain in-flight requests → exit). `terminationGracePeriodSeconds` defaults are sufficient. Slice 7 implementer's discretion.

---

## 7. Implementation Plan Hand-off

This spec is the input to per-slice implementation plans produced via `superpowers:writing-plans`. Each slice gets its own plan PR (or all seven in one batched plan PR — implementer's choice during plan-writing).

When this spec is approved and merged, the next step is:

1. Invoke `superpowers:writing-plans` for slice 1 (or all slices in order).
2. Plan PR(s) reviewed + merged.
3. Implementation worktree(s) opened per `superpowers:using-git-worktrees`.
4. Implementation per `superpowers:test-driven-development` and `superpowers:subagent-driven-development`.
5. PRs raised per `superpowers:finishing-a-development-branch`, with the standing kape-io PR checklist (SBOM scans + summary comment).

---

_End of Phase 6 Implementation Spec._
