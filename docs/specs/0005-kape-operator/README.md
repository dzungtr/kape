# KAPE Operator — Technical Design

**Status:** Draft
**Author:** Dzung Tran
**Session:** 5 — Kape Operator Technical Design
**Created:** 2026-03-23
**Last Updated:** 2026-04-12 (rev 3 — KapeSkillReconciler added, KapeProxy replaces per-tool sidecars)
**Depends on:** `kape-crd-rfc.md`, `kape-handler-runtime-design.md`, `kape-skill-design.md`

---

## Changelog

| Rev | Date       | Change                                                                                                                                                                                                                                                                                  |
| --- | ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 3   | 2026-04-12 | KapeSkillReconciler added. KapeProxy replaces per-tool kapetool sidecar injection. Operator now computes union of handler + skill tool refs and renders kapeproxy-config. Lazy skill file ConfigMap added. Rollout hash extended. Labels extended. RBAC extended. kape-config extended. |
| 2   | 2026-03-23 | CRD RFC rev 4 applied                                                                                                                                                                                                                                                                   |
| 1   | 2026-03-23 | Initial draft                                                                                                                                                                                                                                                                           |

---

## Table of Contents

1. [Overview](#1-overview)
2. [Operator Architecture](#2-operator-architecture)
3. [Go Project Structure](#3-go-project-structure)
4. [KapeToolReconciler](#4-kapetoolreconciler)
5. [KapeSchemaReconciler](#5-kapeschemareconciler)
6. [KapeSkillReconciler](#6-kapeskillreconciler)
7. [KapeHandlerReconciler](#7-kapehandlerreconciler)
8. [Handler ServiceAccount](#8-handler-serviceaccount)
9. [Operator Deployment](#9-operator-deployment)
10. [Operator RBAC](#10-operator-rbac)
11. [Leader Election](#11-leader-election)
12. [Prometheus Metrics](#12-prometheus-metrics)
13. [Error Handling and Requeue Strategy](#13-error-handling-and-requeue-strategy)
14. [kape-config ConfigMap Reference](#14-kape-config-configmap-reference)
15. [Decision Registry](#15-decision-registry)

---

## 1. Overview

The Kape Operator is a Kubernetes controller built with controller-runtime. It manages four CRD types — `KapeHandler`, `KapeTool`, `KapeSchema`, and `KapeSkill` — and is the sole component responsible for infrastructure provisioning, configuration materialisation, and lifecycle management.

**Design principle:** The handler runtime never reads Kubernetes CRDs directly. The operator fully materialises everything the runtime needs into a mounted `settings.toml`, environment variables, a `kapeproxy-config` ConfigMap, and (if lazy skills exist) a skills file ConfigMap — all before pods start.

### 1.1 Operator Responsibilities

- Provision Qdrant StatefulSets for `KapeTool` resources of `type: memory`
- Render and maintain handler `settings.toml` ConfigMaps from `KapeHandler` specs, including assembled system prompt with eager skill instructions and lazy skill preamble
- Render `kapeproxy-config` ConfigMaps from the union of handler + skill tool refs
- Render lazy skill file ConfigMaps (`kape-skills-{handler-name}`) for `lazyLoad: true` skills
- Inject exactly **one `kapeproxy` sidecar** per handler pod (replaces N per-tool `kapetool` sidecars)
- Create and manage handler Deployments with correct volume mounts
- Generate KEDA ScaledObjects for NATS-based autoscaling
- Validate all dependencies (`KapeSchema`, `KapeTools`, `KapeSkills`) before deploying handlers
- Protect `KapeSchema` and `KapeSkill` resources from deletion while referenced by handlers
- Emit Kubernetes Events and Prometheus metrics for observability
- Maintain accurate status conditions on all four CRD types

### 1.2 What the Operator Does NOT Do

- Manage MCP server lifecycle
- Read or write runtime telemetry
- Create RBAC for handler pods — handler ServiceAccounts have zero permissions
- Manage NATS JetStream configuration
- Inject per-tool `kapetool` sidecars — replaced by single `kapeproxy` sidecar

### 1.3 Scope

Cluster-scoped — watches and manages CRDs across all namespaces.

---

## 2. Operator Architecture

### 2.1 Reconciler Overview

| Reconciler              | Watches       | Manages                                                                                                                                  |
| ----------------------- | ------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `KapeToolReconciler`    | `KapeTool`    | Qdrant StatefulSet + Service (type: memory), MCP endpoint health probe (type: mcp), status                                               |
| `KapeSchemaReconciler`  | `KapeSchema`  | Validation, deletion protection finalizer, schema hash, handler rollout signalling                                                       |
| `KapeSkillReconciler`   | `KapeSkill`   | Validation, deletion protection finalizer, tool readiness gate, handler rollout signalling                                               |
| `KapeHandlerReconciler` | `KapeHandler` | ConfigMap (settings.toml), kapeproxy-config, skills ConfigMap, Deployment + kapeproxy sidecar, ServiceAccount, KEDA ScaledObject, status |

### 2.2 Resource Ownership

| Owner CRD                 | Owned Resources                                                                                                                                                                                                                         | On Delete                                  |
| ------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------ |
| `KapeTool` (type: memory) | StatefulSet `kape-memory-{name}`, Service, PVC                                                                                                                                                                                          | StatefulSet + PVC deleted. PV orphaned.    |
| `KapeHandler`             | ConfigMap `kape-handler-{name}`, ConfigMap `kapeproxy-config-{name}`, ConfigMap `kape-skills-{name}` (if lazy skills exist), Deployment `kape-handler-{name}`, ServiceAccount `kape-handler-{name}`, ScaledObject `kape-handler-{name}` | All owned resources deleted automatically. |

### 2.3 Cross-Resource Watches

| Watch target | Filter                      | Map function                                                   |
| ------------ | --------------------------- | -------------------------------------------------------------- |
| `KapeTool`   | Any spec or status change   | List `KapeHandlers` with label `kape.io/tool-ref-{name}=true`  |
| `KapeSchema` | `status.schemaHash` changes | List `KapeHandlers` with label `kape.io/schema-ref={name}`     |
| `KapeSkill`  | Any spec change             | List `KapeHandlers` with label `kape.io/skill-ref-{name}=true` |

---

## 3. Go Project Structure

Unchanged from rev 2 with the following additions:

```
infra/api/v1alpha1/
  kapeskill_types.go        ← NEW — KapeSkill CRD Go types

infra/ports/
  skill.go                  ← NEW — SkillRepository interface

infra/k8s/
  skill_repo.go             ← NEW — implements ports.SkillRepository

controller/
  skill.go                  ← NEW — KapeSkillReconciler (thin controller-runtime wrapper)

controller/reconcile/
  skill.go                  ← NEW — KapeSkill reconcile logic
```

The domain module gains no new types — `KapeSkill` is a simple content resource with no business logic beyond existence and tool readiness validation. The reconcile logic lives in `controller/reconcile/skill.go` directly.

---

## 4. KapeToolReconciler

Unchanged from rev 2. See original section 4 for full specification.

The `kapetool` sidecar injection that was previously triggered by `KapeToolReconciler` is **removed**. Sidecar injection is now handled entirely by `KapeHandlerReconciler` via the unified `kapeproxy` model. `KapeToolReconciler` retains its health probe and Qdrant provisioning responsibilities.

---

## 5. KapeSchemaReconciler

Unchanged from rev 2. See original section 5 for full specification.

---

## 6. KapeSkillReconciler

New in rev 3. Minimal responsibilities — `KapeSkill` is a content resource, not an infrastructure resource.

### 6.1 Reconcile Steps

```
Reconcile(KapeSkill)
  1. Validate spec.instruction is non-empty
       → status.conditions[Ready]=False, reason: InstructionEmpty
  2. Validate spec.description is non-empty
       → status.conditions[Ready]=False, reason: DescriptionEmpty
  3. For each tool in spec.tools[]:
       KapeTool exists → else status.conditions[Ready]=False, reason: ToolNotFound
         message: "KapeTool order-mcp not found"
       KapeTool.status.conditions[Ready]=True
         → else status.conditions[Ready]=False, reason: ToolNotReady
         message: "KapeTool order-mcp: MCPEndpointUnreachable"
  4. Manage finalizer: kape.io/skill-protection
  5. Handle deletion (if DeletionTimestamp set):
       List KapeHandlers cluster-wide with label kape.io/skill-ref-{name}=true
       If referencing handlers exist: block deletion, surface names in message
       If no references: remove finalizer, GC proceeds
  6. Set status.conditions[Ready]=True if all checks pass
  7. RequeueAfter: 30s — periodic tool readiness refresh
```

### 6.2 KapeSkill Status Shape

```yaml
status:
  conditions:
    - type: Ready
      status: "True" | "False"
      reason: Ready | InstructionEmpty | DescriptionEmpty | ToolNotFound | ToolNotReady | ReferencedByHandlers
      message: "KapeTool order-mcp: MCPEndpointUnreachable"
      lastTransitionTime: "..."
```

### 6.3 Handler Rollout Signalling

`KapeHandlerReconciler` watches `KapeSkill` via a secondary watch (label `kape.io/skill-ref-{name}=true`). Any spec change to a skill re-enqueues affected handlers. The handler reconciler includes `KapeSkill.spec` in the rollout hash — a change triggers a Deployment rollout, causing handler pods to pick up updated skill content.

---

## 7. KapeHandlerReconciler

### 7.1 Full Reconcile Flow

```
Reconcile(KapeHandler)
  │
  ▼
1. FETCH — get KapeHandler, return if not found
  │
  ▼
2. VALIDATE DEPENDENCIES (hard gate)
   a. KapeSchema referenced by spec.schemaRef exists AND Ready
      → False: DependenciesReady=False, reason=KapeSchemaInvalid
   b. foreach tool in spec.tools[]:
        KapeTool exists AND Ready
        → False: DependenciesReady=False, reason=KapeToolNotReady
   c. foreach skill in spec.skills[]:            ← NEW
        KapeSkill exists
        → False: DependenciesReady=False, reason=KapeSkillNotFound
          message: "KapeSkill check-order-events not found"
        KapeSkill.status.conditions[Ready]=True
        → False: DependenciesReady=False, reason=KapeSkillNotReady
          message: "KapeSkill check-order-events: KapeTool order-mcp not Ready"
   All pass: DependenciesReady=True
  │
  ▼
3. VALIDATE SCALING CONFIG (unchanged)
  │
  ▼
4. COMPUTE HASHES
   toolMap = union of handler spec.tools[] + all skill spec.tools[]  ← CHANGED
             deduplicated by KapeTool name

   rolloutHash = sha256(
     KapeHandler.spec +
     KapeSchema.spec +
     foreach tool in toolMap: KapeTool.spec +    ← CHANGED — unified toolMap
     foreach skill in spec.skills[]: KapeSkill.spec  ← NEW
   )
  │
  ▼
5. RENDER settings.toml                          ← CHANGED
   a. Render handler's own spec.llm.systemPrompt (Jinja2 raw — runtime resolves)
   b. For each skill in spec.skills[] (declaration order):
        fetch KapeSkill
        if lazyLoad: false → append skill.instruction to system prompt (with --- separator)
        if lazyLoad: true  → collect into lazy skill list
   c. If lazy skills exist → append lazy skill preamble to system prompt:
        "Available skills (call load_skill ...):\n- {name}: {description}\n..."
   d. Write [proxy] section with endpoint: http://localhost:8080
      Remove [tools.*] mcp sections — replaced by kapeproxy-config
      Retain [tools.*] memory sections (Qdrant endpoints)
   e. Write ConfigMap kape-handler-{name}
  │
  ▼
6. RENDER kapeproxy-config                       ← NEW
   a. Build toolMap (computed in step 4)
   b. For each KapeTool in toolMap:
        emit upstream entry with url, transport, allowedTools, redaction, audit
   c. Write ConfigMap kapeproxy-config-{handler-name}
  │
  ▼
7. RENDER lazy skill files                       ← NEW
   If any skill in spec.skills[] has lazyLoad: true:
     For each lazy skill:
       Add {skill.name}.txt = skill.spec.instruction to ConfigMap data
     Write ConfigMap kape-skills-{handler-name}
   If no lazy skills: skip (do not create ConfigMap)
  │
  ▼
8. RECONCILE SERVICEACCOUNT (unchanged)
  │
  ▼
9. RECONCILE DEPLOYMENT                          ← CHANGED
   a. Build desired Deployment:
      - kapehandler container (image from kape-config[kapehandler.image/version])
        volumeMounts:
          - /etc/kape (from kape-handler-{name} ConfigMap)
          - /etc/kape/skills (from kape-skills-{name} ConfigMap)
            ONLY if at least one lazyLoad: true skill exists
      - kapeproxy container (image from kape-config[kapeproxy.image/version])
        volumeMounts:
          - /etc/kapeproxy (from kapeproxy-config-{name} ConfigMap)
        ports: [8080]
        resources: requests 100m/128Mi, limits 500m/256Mi
      NOTE: No per-tool kapetool sidecar containers
      - Pod annotation: kape.io/rollout-hash={rolloutHash}
      - automountServiceAccountToken: false
   b. Volumes:
      - kape-handler-{name} ConfigMap → /etc/kape
      - kapeproxy-config-{name} ConfigMap → /etc/kapeproxy
      - kape-skills-{name} ConfigMap → /etc/kape/skills  (only if lazy skills exist)
   c. Detect consumer name change vs existing ScaledObject → delete ScaledObject if changed
   d. Create or patch Deployment (server-side apply)
   e. Read Deployment status → set DeploymentAvailable condition
  │
  ▼
10. RECONCILE KEDA SCALEDOBJECT (unchanged)
  │
  ▼
11. SYNC LABELS onto KapeHandler
    kape.io/schema-ref={spec.schemaRef}
    kape.io/tool-ref-{toolname}=true   (one per entry in unified toolMap)  ← CHANGED
    kape.io/skill-ref-{skillname}=true (one per entry in spec.skills[])    ← NEW
  │
  ▼
12. COMPUTE state ROLLUP (unchanged)
  │
  ▼
13. PATCH KapeHandler.status
  │
  ▼
14. RETURN with appropriate RequeueAfter
```

### 7.2 System Prompt Assembly Logic

```go
prompt := handler.Spec.LLM.SystemPrompt

eagerInstructions := []string{}
lazySkills        := []SkillMeta{}

for _, skillRef := range handler.Spec.Skills {
    skill := fetchKapeSkill(skillRef.Ref)
    if !skill.Spec.LazyLoad {
        eagerInstructions = append(eagerInstructions, skill.Spec.Instruction)
    } else {
        lazySkills = append(lazySkills, SkillMeta{
            Name:        skill.Name,
            Description: skill.Spec.Description,
        })
    }
}

// Append eager skill instructions
if len(eagerInstructions) > 0 {
    prompt += "\n\n---\n\n"
    prompt += strings.Join(eagerInstructions, "\n\n---\n\n")
}

// Append lazy skill preamble
if len(lazySkills) > 0 {
    if len(eagerInstructions) > 0 {
        prompt += "\n\n---\n\n"
    } else {
        prompt += "\n\n"
    }
    lines := []string{
        "Available skills (call load_skill with the skill name to retrieve full instructions):",
    }
    for _, s := range lazySkills {
        lines = append(lines, fmt.Sprintf("- %s: %s", s.Name, s.Description))
    }
    lines = append(lines, "\nWhen you determine a skill is relevant, call load_skill with its name before proceeding.")
    prompt += strings.Join(lines, "\n")
}

// Write assembled prompt into settings.toml [llm] system_prompt
```

### 7.3 kapeproxy-config Rendering

```go
type ProxyConfig struct {
    Upstreams map[string]UpstreamConfig `yaml:"upstreams"`
}

type UpstreamConfig struct {
    URL          string      `yaml:"url"`
    Transport    string      `yaml:"transport"`
    AllowedTools []string    `yaml:"allowedTools"`
    Redaction    RedactionConfig `yaml:"redaction,omitempty"`
    Audit        bool        `yaml:"audit"`
}

func renderProxyConfig(toolMap map[string]KapeTool) ProxyConfig {
    cfg := ProxyConfig{Upstreams: map[string]UpstreamConfig{}}
    for name, tool := range toolMap {
        if tool.Spec.Type != "mcp" {
            continue  // memory and event-publish tools not in kapeproxy-config
        }
        cfg.Upstreams[name] = UpstreamConfig{
            URL:          tool.Spec.MCP.Upstream.URL,
            Transport:    tool.Spec.MCP.Upstream.Transport,
            AllowedTools: tool.Spec.MCP.AllowedTools,
            Redaction:    tool.Spec.MCP.Redaction,
            Audit:        tool.Spec.MCP.Audit.Enabled,
        }
    }
    return cfg
}
```

### 7.4 Tool Union Computation

```go
// Build unified tool map keyed by KapeTool name — deduplicated
toolMap := map[string]KapeTool{}

// From handler spec.tools[]
for _, ref := range handler.Spec.Tools {
    tool := fetchKapeTool(ref.Ref)
    toolMap[tool.Name] = tool
}

// From each skill's spec.tools[]
for _, skillRef := range handler.Spec.Skills {
    skill := fetchKapeSkill(skillRef.Ref)
    for _, ref := range skill.Spec.Tools {
        tool := fetchKapeTool(ref.Ref)
        toolMap[tool.Name] = tool  // duplicate KapeTool name = no-op
    }
}
// toolMap fed into: kapeproxy-config rendering, rollout hash, label sync
```

### 7.5 settings.toml Structure

See `kape-handler-runtime-design.md` section 3.2 for full example.

Key changes from rev 2:

- `[tools.*]` mcp sections removed — replaced by `[proxy]` section
- `[tools.*]` memory sections retained unchanged
- System prompt now contains assembled eager skill instructions + lazy preamble

### 7.6 KapeHandler Status Shape

```yaml
status:
  state: Pending | Active | Degraded | Failed
  conditions:
    - type: Ready
      status: "True" | "False"
      reason: DependenciesNotReady | DeploymentUnavailable | Ready
    - type: DependenciesReady
      status: "True" | "False"
      reason: KapeToolNotReady | KapeSchemaInvalid | KapeSkillNotFound | KapeSkillNotReady | Ready
      message: "KapeSkill check-order-events: KapeTool order-mcp not Ready"
    - type: DeploymentAvailable
      status: "True" | "False"
      reason: MinimumReplicasUnavailable | Available
    - type: ScalingConfigured
      status: "True" | "False"
      reason: InvalidScalingConfig | KEDAScaledObjectNotReady | Configured
  replicas: 2
  readyReplicas: 2
  observedGeneration: 5
```

---

## 8. Handler ServiceAccount

Unchanged from rev 2.

---

## 9. Operator Deployment

Unchanged from rev 2.

---

## 10. Operator RBAC

### 10.1 ClusterRole: kape-operator

Additions from rev 2 in bold:

```yaml
rules:
  # KAPE CRDs — KapeSkill added
  - apiGroups: ["kape.io"]
    resources: [kapehandlers, kapetools, kapeschemas, kapeskills]
    verbs: [get, list, watch, update, patch]
  - apiGroups: ["kape.io"]
    resources:
      - kapehandlers/status
      - kapetools/status
      - kapeschemas/status
      - kapeskills/status # NEW
      - kapehandlers/finalizers
      - kapeschemas/finalizers
      - kapeskills/finalizers # NEW
    verbs: [get, update, patch]

  # ConfigMaps — kapeproxy-config and kape-skills ConfigMaps added
  # (same verb set, no change to rule — create/update/patch/delete already granted)
  - apiGroups: [""]
    resources: [configmaps]
    verbs: [get, list, watch, create, update, patch, delete]

  # Remaining rules unchanged from rev 2
  - apiGroups: [""]
    resources: [serviceaccounts]
    verbs: [get, list, watch, create, update, patch, delete]
  - apiGroups: [""]
    resources: [secrets]
    verbs: [get, list, watch]
  - apiGroups: [apps]
    resources: [deployments]
    verbs: [get, list, watch, create, update, patch, delete]
  - apiGroups: [apps]
    resources: [statefulsets]
    verbs: [get, list, watch, create, update, patch, delete]
  - apiGroups: [""]
    resources: [services]
    verbs: [get, list, watch, create, update, patch, delete]
  - apiGroups: [""]
    resources: [persistentvolumeclaims]
    verbs: [get, list, watch, create, delete]
  - apiGroups: [keda.sh]
    resources: [scaledobjects]
    verbs: [get, list, watch, create, update, patch, delete]
  - apiGroups: [""]
    resources: [events]
    verbs: [create, patch]
```

### 10.2 Role: kape-operator-leader-election

Unchanged from rev 2.

### 10.3 Kubernetes Events Emitted

All events from rev 2, plus:

| Resource      | Reason              | Type    | Message                                                                  |
| ------------- | ------------------- | ------- | ------------------------------------------------------------------------ |
| `KapeHandler` | `KapeSkillNotFound` | Warning | `KapeSkill check-order-events not found`                                 |
| `KapeHandler` | `KapeSkillNotReady` | Warning | `KapeSkill check-order-events: KapeTool order-mcp not Ready`             |
| `KapeSkill`   | `DeletionBlocked`   | Warning | `Cannot delete: referenced by handlers: [order-payment-failure-handler]` |
| `KapeSkill`   | `SkillValid`        | Normal  | `KapeSkill validated successfully`                                       |

---

## 11. Leader Election

Unchanged from rev 2.

---

## 12. Prometheus Metrics

### 12.1 Built-in (controller-runtime)

Unchanged from rev 2. `KapeSkillReconciler` automatically gains controller-runtime built-in metrics.

### 12.2 Custom Metrics

All metrics from rev 2, plus:

| Metric                                | Description                          |
| ------------------------------------- | ------------------------------------ |
| `kape_skills_total{namespace, ready}` | Gauge — KapeSkill count by readiness |

---

## 13. Error Handling and Requeue Strategy

Unchanged from rev 2. `KapeSkillReconciler` follows same pattern:

| Condition                   | RequeueAfter                          |
| --------------------------- | ------------------------------------- |
| Tool not found or not ready | 30s                                   |
| Skill valid                 | 30s (periodic tool readiness refresh) |
| Invalid spec (terminal)     | No requeue                            |

---

## 14. kape-config ConfigMap Reference

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kape-config
  namespace: kape-system
data:
  # Qdrant
  qdrant.version: "v1.14.0"
  qdrant.storageClass: "standard"
  qdrant.embeddingDimensions: "1536"

  # KapeProxy — replaces kapetool sidecar entries
  kapeproxy.image: "kape/kapeproxy" # NEW
  kapeproxy.version: "v0.1.0" # NEW

  # Handler runtime
  kapehandler.image: "kape/handler"
  kapehandler.version: "v0.1.0"

  # NATS
  nats.monitoringEndpoint: "http://nats.kape-system:8222"

  # Handler defaults
  handler.maxIterations: "50"

  # Removed: kapetool.image, kapetool.version — kapetool sidecar no longer used
```

---

## 15. Decision Registry

All decisions from rev 2 remain valid. The following decisions are added or changed in rev 3.

### KapeSkillReconciler (new)

| Decision                | Value                                                                               |
| ----------------------- | ----------------------------------------------------------------------------------- |
| Validation              | `spec.instruction` non-empty, `spec.description` non-empty                          |
| Tool gate               | All `spec.tools[]` KapeTools must exist and be Ready                                |
| Deletion protection     | Finalizer `kape.io/skill-protection` blocks delete while handlers reference skill   |
| Handler discovery       | Label `kape.io/skill-ref-{name}=true` on KapeHandler                                |
| Handler rollout trigger | Any KapeSkill.spec change → KapeHandlerReconciler re-enqueues, updates rollout-hash |
| RequeueAfter            | 30s — periodic tool readiness refresh                                               |

### KapeHandlerReconciler (changed)

| Decision                     | Value                                                                                                                               |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| Dependency gate              | Extended: KapeSkill must exist AND be Ready (KapeSkill Ready = all its tools exist and Ready)                                       |
| Tool union                   | Operator computes union of handler `spec.tools[]` + all skill `spec.tools[]`, deduplicates by KapeTool name                         |
| System prompt assembly       | Handler systemPrompt → eager skill instructions (declaration order) → lazy skill preamble                                           |
| Rollout hash                 | Extended to include all KapeSkill.spec entries in spec.skills[]                                                                     |
| Label sync                   | `kape.io/skill-ref-{skillname}=true` added per skill reference                                                                      |
| Sidecar injection            | **One `kapeproxy` sidecar always.** No per-tool `kapetool` sidecars.                                                                |
| kapeproxy-config             | Rendered from unified toolMap (mcp-type tools only)                                                                                 |
| Lazy skill ConfigMap         | `kape-skills-{handler-name}` — one file per lazy skill. Only created if lazy skills exist. Volume only mounted if ConfigMap exists. |
| skills.lazy in settings.toml | **Not used.** System prompt contains all information. load_skill reads filesystem directly.                                         |

### KapeProxy (new component)

| Decision                 | Value                                                                                                 |
| ------------------------ | ----------------------------------------------------------------------------------------------------- |
| Model                    | One sidecar per pod. Replaces N per-tool kapetool sidecars.                                           |
| Language                 | Go                                                                                                    |
| Port                     | `:8080`                                                                                               |
| Tool namespace separator | Double underscore: `{kapetool-name}__{tool-name}`                                                     |
| Tool filtering           | At startup via tools/list from upstream. Unlisted tools never registered.                             |
| Collision prevention     | KapeTool name prefix guarantees uniqueness across upstreams.                                          |
| Unreachable upstream     | Log, mark unavailable, continue. Pod starts. Tool calls return MCP error.                             |
| OTEL                     | Centralised in kapeproxy. All tool call spans emitted here. W3C TraceContext propagated from handler. |
| Config                   | `/etc/kapeproxy/config.yaml` from `kapeproxy-config-{handler-name}` ConfigMap                         |
| kape-config keys         | `kapeproxy.image`, `kapeproxy.version`                                                                |

### Removed

| Item                                                | Reason                                   |
| --------------------------------------------------- | ---------------------------------------- |
| Per-tool `kapetool` sidecar injection               | Replaced by single `kapeproxy` sidecar   |
| `kapetool.image`, `kapetool.version` in kape-config | kapetool image no longer used            |
| `[tools.*]` mcp sections in settings.toml           | Replaced by `[proxy]` section            |
| KAPETOOL\_\* env vars on sidecar containers         | kapeproxy reads from mounted config.yaml |
