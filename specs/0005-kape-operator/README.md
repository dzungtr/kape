# KAPE Operator — Technical Design

**Status:** Draft
**Author:** Dzung Tran
**Session:** 5 — Kape Operator Technical Design
**Created:** 2026-03-23
**Last Updated:** 2026-03-23 (rev 2 — CRD RFC rev 4 applied)
**Depends on:** `kape-crd-rfc.md` (rev 4), `kape-handler-runtime-design.md`

---

## Changelog

| Rev | Date       | Change                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| --- | ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 2   | 2026-03-23 | CRD RFC rev 4 applied. Removed `systemPromptRef` ConfigMap watch and hard gate. Removed `$prompt` schema merge logic. Removed `guardrails.maxCallsPerMinute` from tool refs. Updated `actions[]` to inline `type`/`data`/`condition` (simpleeval). Updated hard gate to check `spec.tools[]` only. Updated `KapeTool` sidecar injection to read `mcp.upstream`, `allowedTools`, `redaction`, `audit`. Simplified `settings.toml` rendering. Updated rollout hash inputs. |
| 1   | 2026-03-23 | Initial draft                                                                                                                                                                                                                                                                                                                                                                                                                                                            |

---

## Table of Contents

1. [Overview](#1-overview)
2. [Operator Architecture](#2-operator-architecture)
3. [Go Project Structure](#3-go-project-structure)
4. [KapeToolReconciler](#4-kapetoolreconciler)
5. [KapeSchemaReconciler](#5-kapeschemareconciler)
6. [KapeHandlerReconciler](#6-kapehandlerreconciler)
7. [Handler ServiceAccount](#7-handler-serviceaccount)
8. [Operator Deployment](#8-operator-deployment)
9. [Operator RBAC](#9-operator-rbac)
10. [Leader Election](#10-leader-election)
11. [Prometheus Metrics](#11-prometheus-metrics)
12. [Error Handling and Requeue Strategy](#12-error-handling-and-requeue-strategy)
13. [kape-config ConfigMap Reference](#13-kape-config-configmap-reference)
14. [Decision Registry](#14-decision-registry)

---

## 1. Overview

The Kape Operator is a Kubernetes controller built with controller-runtime. It manages three CRD types — `KapeHandler`, `KapeTool`, and `KapeSchema` — and is the sole component responsible for infrastructure provisioning, configuration materialisation, and lifecycle management.

**Design principle:** The handler runtime never reads Kubernetes CRDs directly. The operator fully materialises everything the runtime needs into a mounted `settings.toml` and environment variables before pods start.

### 1.1 Operator Responsibilities

- Provision Qdrant StatefulSets for `KapeTool` resources of `type: memory`
- Render and maintain handler `settings.toml` ConfigMaps from `KapeHandler` specs
- Inject one `kapetool` sidecar container per `mcp`-type `KapeTool` referenced in `spec.tools[]`
- Create and manage handler Deployments with correct sidecar injection
- Generate KEDA ScaledObjects for NATS-based autoscaling
- Validate all dependencies (`KapeSchema`, `KapeTools` in `spec.tools[]`) before deploying handlers
- Protect `KapeSchema` resources from deletion while referenced by handlers
- Emit Kubernetes Events and Prometheus metrics for observability
- Maintain accurate status conditions on all three CRD types

### 1.2 What the Operator Does NOT Do

- Manage MCP server lifecycle — engineer deploys MCP servers independently
- Read or write runtime telemetry — dashboard reads `kape-task-service` directly
- Create RBAC for handler pods — handler ServiceAccounts have zero permissions
- Manage NATS JetStream configuration — assumed pre-deployed
- Watch `systemPromptRef` ConfigMaps — `systemPrompt` is inline in `spec.llm.systemPrompt` (CRD RFC rev 4)
- Merge `$prompt` fields into schemas — removed in CRD RFC rev 4; `KapeSchema` owns only the decision shape

### 1.3 Scope

The operator is **cluster-scoped** — it watches and manages `KapeHandler`, `KapeTool`, and `KapeSchema` resources across all namespaces. v1 ships with `kape-system` examples only, but the operator is designed for multi-namespace use from day one.

---

## 2. Operator Architecture

### 2.1 Reconciler Overview

| Reconciler              | Watches       | Manages                                                                                                              |
| ----------------------- | ------------- | -------------------------------------------------------------------------------------------------------------------- |
| `KapeToolReconciler`    | `KapeTool`    | Qdrant StatefulSet + Service (type: memory), MCP endpoint health probe (type: mcp), event-publish validation, status |
| `KapeSchemaReconciler`  | `KapeSchema`  | Validation, deletion protection finalizer, schema hash, handler rollout signalling                                   |
| `KapeHandlerReconciler` | `KapeHandler` | ConfigMap (settings.toml), Deployment + sidecar injection, ServiceAccount, KEDA ScaledObject, status                 |

### 2.2 Resource Ownership

All resources created by the operator carry an owner reference to their parent CRD. Kubernetes GC handles cascade deletion automatically.

| Owner CRD                 | Owned Resources                                                                                                                             | On Delete                                                     |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `KapeTool` (type: memory) | StatefulSet `kape-memory-{name}`, Service `kape-memory-{name}`, PVC                                                                         | StatefulSet + PVC deleted. PV orphaned — CSI handles reclaim. |
| `KapeHandler`             | ConfigMap `kape-handler-{name}`, Deployment `kape-handler-{name}`, ServiceAccount `kape-handler-{name}`, ScaledObject `kape-handler-{name}` | All owned resources deleted automatically.                    |

### 2.3 Cross-Resource Watches

`KapeHandlerReconciler` watches two foreign resource types and maps changes back to the affected handler. The `systemPromptRef` ConfigMap watch from the earlier design is removed — `systemPrompt` is now inline in the CRD spec.

| Watch target | Filter                      | Map function                                                  |
| ------------ | --------------------------- | ------------------------------------------------------------- |
| `KapeTool`   | Any spec or status change   | List `KapeHandlers` with label `kape.io/tool-ref-{name}=true` |
| `KapeSchema` | `status.schemaHash` changes | List `KapeHandlers` with label `kape.io/schema-ref={name}`    |

---

## 3. Go Project Structure

The operator follows domain-driven design with dependency inversion. Three Go modules enforce strict layering — `domain` has zero external dependencies, `infra` adapts external systems to domain interfaces, `controller` wires them together via thin reconcilers.

### 3.1 Module Layout

```
github.com/kape-io/kape-operator/
├── domain/                           — pure Go, zero external dependencies
│   ├── go.mod
│   ├── handler/
│   │   ├── handler.go                — Handler struct, State enum, business rules
│   │   └── state.go                  — state machine: Pending → Active → Degraded → Failed
│   ├── tool/
│   │   ├── tool.go                   — Tool struct, ToolType enum (mcp|memory|event-publish)
│   │   └── qdrant.go                 — Qdrant collection naming, storage rules
│   ├── schema/
│   │   ├── schema.go                 — Schema struct, validation rules
│   │   └── validator.go              — JSON Schema validation (pure)
│   └── config/
│       └── config.go                 — KapeConfig value object
│
├── infra/                            — Kubernetes + external adapters
│   ├── go.mod                        — imports controller-runtime, client-go, etc.
│   ├── api/
│   │   └── v1alpha1/
│   │       ├── kapehandler_types.go  — controller-gen structs
│   │       ├── kapetool_types.go
│   │       ├── kapeschema_types.go
│   │       └── groupversion_info.go
│   ├── ports/                        — all outbound interfaces
│   │   ├── handler.go                — HandlerRepository, DeploymentPort, ConfigMapPort, ...
│   │   ├── tool.go                   — ToolRepository, QdrantPort, MCPProbePort
│   │   └── schema.go                 — SchemaRepository, EventEmitterPort
│   ├── k8s/                          — Kubernetes adapter implementations
│   │   ├── handler_repo.go           — implements ports.HandlerRepository
│   │   ├── tool_repo.go              — implements ports.ToolRepository
│   │   ├── schema_repo.go            — implements ports.SchemaRepository
│   │   ├── deployment.go             — implements ports.DeploymentPort
│   │   ├── configmap.go              — implements ports.ConfigMapPort
│   │   ├── scaledobject.go           — implements ports.ScaledObjectPort
│   │   ├── statefulset.go            — implements ports.QdrantStatefulSetPort
│   │   ├── serviceaccount.go         — implements ports.ServiceAccountPort
│   │   └── events.go                 — implements ports.EventEmitterPort
│   ├── qdrant/
│   │   └── client.go                 — implements ports.QdrantPort
│   ├── probe/
│   │   └── mcp.go                    — implements ports.MCPProbePort
│   ├── toml/
│   │   └── renderer.go               — renders settings.toml from domain types
│   └── metrics/
│       └── prometheus.go             — implements ports.MetricsPort
│
├── controller/                       — thin controller-runtime reconcilers
│   ├── go.mod                        — imports controller-runtime, infra/ports, domain
│   ├── handler.go                    — KapeHandlerReconciler (Reconcile method only)
│   ├── tool.go                       — KapeToolReconciler
│   ├── schema.go                     — KapeSchemaReconciler
│   ├── watches.go                    — secondary watch setup, map functions
│   ├── indexer.go                    — label index registration
│   └── reconcile/                    — full reconcile logic per resource type
│       ├── handler.go
│       ├── tool.go
│       └── schema.go
│
└── cmd/                              — binary entry point
    ├── go.mod
    └── main.go                       — ff config, wire adapters, start manager
```

> **Note:** `domain/handler/schema_merger.go` from the earlier design is removed. The `$prompt` field merge pattern was dropped in CRD RFC rev 4. `KapeSchema` owns only the decision shape and the operator serialises `spec.jsonSchema` directly into `settings.toml` without merging.

### 3.2 Dependency Rule

```
cmd        →  controller, infra, domain
controller →  infra/ports (interfaces), domain (structs)
infra      →  domain (structs only)
domain     →  stdlib only
```

`domain` never imports `infra` or `controller`. Interfaces live in `infra/ports/` — the infra boundary owns both the contract and the implementation. Domain reconcile logic in `controller/reconcile/` can be unit tested by injecting mock port implementations with no Kubernetes API server required.

### 3.3 CRD Code Generation

CRD YAML is generated from Go types using `controller-gen` with kubebuilder markers. Go types in `infra/api/v1alpha1/` are the source of truth. CRD YAML is a build artifact, not a source file.

```bash
controller-gen rbac:roleName=kape-operator crd webhook paths=./infra/... \
    output:crd:artifacts:config=config/crd/bases
```

### 3.4 ff Configuration

The operator binary uses the `ff` library for flag-first configuration.

Priority chain (highest to lowest):

```
CLI flags  →  KAPE_OPERATOR_* env vars  →  config.yaml  →  defaults
```

Config file: `/etc/kape-operator/config.yaml` (YAML format, mounted from a ConfigMap).

| Flag                          | Env var                                   | Default                          | Description                        |
| ----------------------------- | ----------------------------------------- | -------------------------------- | ---------------------------------- |
| `--metrics-bind-address`      | `KAPE_OPERATOR_METRICS_BIND_ADDRESS`      | `:8080`                          | Metrics server address             |
| `--health-probe-bind-address` | `KAPE_OPERATOR_HEALTH_PROBE_BIND_ADDRESS` | `:8081`                          | Health probe address               |
| `--leader-elect`              | `KAPE_OPERATOR_LEADER_ELECT`              | `true`                           | Enable leader election             |
| `--max-concurrent-reconciles` | `KAPE_OPERATOR_MAX_CONCURRENT_RECONCILES` | `3`                              | Parallel reconciles per controller |
| `--kape-config-namespace`     | `KAPE_OPERATOR_KAPE_CONFIG_NAMESPACE`     | `kape-system`                    | Namespace of kape-config ConfigMap |
| `--kape-config-name`          | `KAPE_OPERATOR_KAPE_CONFIG_NAME`          | `kape-config`                    | Name of kape-config ConfigMap      |
| `--config`                    | `KAPE_OPERATOR_CONFIG`                    | `/etc/kape-operator/config.yaml` | Path to YAML config file           |

### 3.5 main.go Wiring

```go
func main() {
    fs := flag.NewFlagSet("kape-operator", flag.ExitOnError)
    cfg := &config{}

    fs.StringVar(&cfg.MetricsAddr,            "metrics-bind-address",      ":8080",       "")
    fs.StringVar(&cfg.HealthProbeAddr,         "health-probe-bind-address", ":8081",       "")
    fs.BoolVar  (&cfg.LeaderElect,             "leader-elect",              true,          "")
    fs.IntVar   (&cfg.MaxConcurrentReconciles, "max-concurrent-reconciles", 3,             "")
    fs.StringVar(&cfg.KapeConfigNamespace,     "kape-config-namespace",     "kape-system", "")
    fs.StringVar(&cfg.KapeConfigName,          "kape-config-name",          "kape-config", "")

    ff.Parse(fs, os.Args[1:],
        ff.WithEnvVarPrefix("KAPE_OPERATOR"),
        ff.WithConfigFileFlag("config"),
        ff.WithConfigFileParser(ffyaml.Parser),
        ff.WithAllowMissingConfigFile(true),
    )

    mgr := buildManager(cfg)
    k8sClient     := mgr.GetClient()
    eventRecorder := mgr.GetEventRecorderFor("kape-operator")
    metricsRec    := metrics.NewPrometheusRecorder()

    // Repositories and adapters — all implement infra/ports interfaces
    handlerRepo           := k8s.NewHandlerRepository(k8sClient)
    toolRepo              := k8s.NewToolRepository(k8sClient)
    schemaRepo            := k8s.NewSchemaRepository(k8sClient)
    deploymentAdapter     := k8s.NewDeploymentAdapter(k8sClient, eventRecorder)
    configMapAdapter      := k8s.NewConfigMapAdapter(k8sClient, toml.NewRenderer())
    scaledObjectAdapter   := k8s.NewScaledObjectAdapter(k8sClient, eventRecorder)
    serviceAccountAdapter := k8s.NewServiceAccountAdapter(k8sClient)
    statefulSetAdapter    := k8s.NewStatefulSetAdapter(k8sClient, eventRecorder)
    eventEmitter          := k8s.NewEventEmitter(eventRecorder)

    // Domain reconcilers — depend on interfaces only
    handlerRec := domain_handler.NewReconciler(
        handlerRepo, schemaRepo, toolRepo,
        deploymentAdapter, configMapAdapter, scaledObjectAdapter,
        serviceAccountAdapter, eventEmitter, metricsRec,
    )
    toolRec := domain_tool.NewReconciler(
        toolRepo, statefulSetAdapter,
        qdrant.NewClient(), probe.NewMCPProber(),
        eventEmitter, metricsRec,
    )
    schemaRec := domain_schema.NewReconciler(
        schemaRepo, handlerRepo, eventEmitter, metricsRec,
    )

    controller.SetupHandlerReconciler(mgr, handlerRec, cfg.MaxConcurrentReconciles)
    controller.SetupToolReconciler(mgr, toolRec, cfg.MaxConcurrentReconciles)
    controller.SetupSchemaReconciler(mgr, schemaRec, cfg.MaxConcurrentReconciles)

    mgr.Start(ctrl.SetupSignalHandler())
}
```

The thin controller-runtime reconciler:

```go
// controller/handler.go
type KapeHandlerReconciler struct {
    reconciler domain_handler.Reconciler  // interface, not concrete type
}

func (r *KapeHandlerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    return r.reconciler.Reconcile(ctx, req.NamespacedName)
}
```

---

## 4. KapeToolReconciler

The simplest reconciler. Dispatches on `spec.type`. The only reconciler that provisions external infrastructure.

### 4.1 Reconcile Dispatch

```
Reconcile(KapeTool)
  ├── type: mcp           → reconcileMCP()
  ├── type: memory        → reconcileMemory()
  └── type: event-publish → reconcileEventPublish()
```

### 4.2 type: mcp

- Probe `spec.mcp.upstream.url + /health` (configurable, default `/health`), 5s timeout, 3 retries
- Set `status.conditions[Ready]` based on probe result — reason `MCPEndpointUnreachable` with URL and HTTP status in message
- `RequeueAfter: 30s` — periodic health refresh
- Advisory only — `KapeHandlerReconciler` hard-gates on `Ready=True` for initial deployment

The operator also reads `spec.mcp.upstream`, `spec.mcp.allowedTools`, `spec.mcp.redaction`, and `spec.mcp.audit` during handler Deployment reconciliation to inject them as env vars into the `kapetool` sidecar. See section 6.3 for sidecar injection details.

### 4.3 type: memory

One Qdrant StatefulSet per `KapeTool` of type `memory`. Fully isolated — no shared Qdrant instances.

```
reconcileMemory(tool)
  1. Ensure StatefulSet kape-memory-{name} in tool namespace
       Image:     qdrant/qdrant:{kape-config[qdrant.version]}
       Storage:   spec.memory.storage (default 10Gi)
       Class:     kape-config[qdrant.storageClass]
       Resources: 256m CPU request, 512Mi memory request
       Owner:     KapeTool → StatefulSet (GC cascade on delete)
  2. Ensure headless Service kape-memory-{name} (port 6333 HTTP, 6334 gRPC)
       Owner: KapeTool → Service
  3. Wait for StatefulSet ReadyReplicas >= 1
       → set status.conditions[Ready]=True
  4. Write status.qdrantEndpoint: http://kape-memory-{name}.{namespace}:6333
```

On `KapeTool` deletion: owner references cause Kubernetes GC to delete StatefulSet and PVC. PV is orphaned — CSI controller applies its `reclaimPolicy`.

> **Future:** Swap StatefulSet for `QdrantCluster` CRD when qdrant-operator matures. See [github.com/qdrant-operator/qdrant-operator](https://github.com/qdrant-operator/qdrant-operator). The operator change is a drop-in swap in `infra/k8s/statefulset.go`.

### 4.4 type: event-publish

In CRD RFC rev 4, the `event-publish` KapeTool type is retained for named handler-to-handler chaining contracts but is no longer used for action data templating. Actions are fully inline in `spec.actions[]`.

- Validate `spec.eventPublish.type` is a valid CloudEvents type string
- Set `status.conditions[Ready]=True` if valid
- No `RequeueAfter` — purely spec-driven
- The operator does **not** inject sidecars for `event-publish` tools and does **not** gate handler deployment on their readiness

### 4.5 KapeTool Status Shape

```yaml
status:
  conditions:
    - type: Ready
      status: "True" | "False"
      reason: MCPEndpointUnreachable | QdrantNotReady | ValidationFailed | Ready
      message: "human readable detail"
      lastTransitionTime: "..."
  # memory type only:
  qdrantEndpoint: "http://kape-memory-karpenter-memory.kape-system:6333"
```

---

## 5. KapeSchemaReconciler

Zero infrastructure side effects. Validates schema spec, manages a deletion protection finalizer, and signals handler rollouts on schema changes.

The `$prompt` field merge pattern is removed in CRD RFC rev 4. `KapeSchema` owns only the decision shape — the operator serialises `spec.jsonSchema` directly into `settings.toml` without merging.

### 5.1 Reconcile Steps

```
Reconcile(KapeSchema)
  1. Validate spec.jsonSchema
       - Parse as JSON Schema, assert type: object at root
       - Assert all required[] entries exist in properties
       - Assert spec.version is non-empty
       Note: no longer checks for $ prefix on property keys — $prompt removed in rev 4
  2. Manage finalizer: kape.io/schema-protection
  3. Handle deletion (if DeletionTimestamp set):
       - List KapeHandlers cluster-wide with label kape.io/schema-ref={name}
       - If referencing handlers exist: block deletion, surface names in message
       - If no references: remove finalizer, GC proceeds
  4. Compute schemaHash = sha256(spec.jsonSchema + spec.version)
       Write to status.schemaHash
  5. Set status.conditions[Ready]
```

`KapeHandlerReconciler` watches `status.schemaHash` via a secondary watch and re-enqueues affected handlers on change. The handler reconciler then updates the `rollout-hash` pod annotation, triggering a Deployment rollout.

### 5.2 Label Index for Handler Discovery

`KapeHandlerReconciler` sets label `kape.io/schema-ref={schemaName}` on each `KapeHandler` it reconciles. `KapeSchemaReconciler` lists referencing handlers using a standard label selector — no field indexer registration needed.

### 5.3 KapeSchema Status Shape

```yaml
status:
  conditions:
    - type: Ready
      status: "True" | "False"
      reason: Valid | InvalidSchema | ReferencedByHandlers
      message: "Cannot delete: referenced by handlers: [handler-a, handler-b]"
      lastTransitionTime: "..."
  schemaHash: "sha256-abc123..."   # changes trigger handler rollout
  version: v1
```

---

## 6. KapeHandlerReconciler

The most complex reconciler. Owns the Deployment, ConfigMap, ServiceAccount, and KEDA ScaledObject for each `KapeHandler`. Hard-gates on all dependency readiness before creating any infrastructure.

### 6.1 Full Reconcile Flow

```
Reconcile(KapeHandler)
  │
  ▼
1. FETCH — get KapeHandler, return if not found
  │
  ▼
2. VALIDATE DEPENDENCIES (hard gate)
   a. KapeSchema referenced by spec.schemaRef exists AND status.conditions[Ready]=True
      → False: DependenciesReady=False, reason=KapeSchemaInvalid, requeue 30s
   b. foreach tool in spec.tools[] only:
        KapeTool exists AND status.conditions[Ready]=True
        → False: DependenciesReady=False, reason=KapeToolNotReady,
                 message="KapeTool {name}: {tool.status.conditions[Ready].message}"
                 requeue 30s
   Note: spec.actions[] are fully inline — no KapeTool references to gate on.
         systemPromptRef removed — systemPrompt is inline in spec.llm.systemPrompt.
   All pass: DependenciesReady=True
  │
  ▼
3. VALIDATE SCALING CONFIG
   scaleToZero=true AND minReplicas >= 1
   → ScalingConfigured=False, reason=InvalidScalingConfig — do not proceed
  │
  ▼
4. COMPUTE HASHES
   rolloutHash = sha256(
     KapeHandler.spec +
     KapeSchema.spec +
     foreach tool in spec.tools[]: KapeTool.spec
   )
   Note: systemPrompt is inline in spec — already included in KapeHandler.spec hash.
         No separate ConfigMap hash input needed.
   consumerName = strings.ReplaceAll(spec.trigger.type, ".", "-")
  │
  ▼
5. RENDER settings.toml
   - Inline spec.llm.systemPrompt directly (no ConfigMap lookup)
   - Assign sidecar ports positionally for mcp-type tools (8080, 8081, ...)
   - Serialise spec.jsonSchema from KapeSchema directly (no $prompt merge)
   - Serialise spec.actions[] inline (name, type, condition, data)
   - Serialise spec.trigger.replayOnStartup and maxEventAgeSeconds
   - Serialise spec.llm.maxIterations and spec.dryRun
   - Write ConfigMap kape-handler-{name} (create or server-side apply)
  │
  ▼
6. RECONCILE SERVICEACCOUNT
   Ensure kape-handler-{name} exists with owner reference
  │
  ▼
7. RECONCILE DEPLOYMENT
   a. Build desired Deployment:
      - kapehandler container (image from kape-config[kapehandler.image/version])
      - One kapetool sidecar per mcp-type KapeTool in spec.tools[]
        memory-type tools do NOT get a sidecar — runtime connects to Qdrant
        directly via qdrant_endpoint in settings.toml
        Sidecar env vars injected by operator (per tool):
          KAPETOOL_UPSTREAM_URL        = spec.mcp.upstream.url
          KAPETOOL_UPSTREAM_TRANSPORT  = spec.mcp.upstream.transport
          KAPETOOL_ALLOWED_TOOLS       = spec.mcp.allowedTools (JSON array)
          KAPETOOL_REDACTION_INPUT     = spec.mcp.redaction.input (JSON array)
          KAPETOOL_REDACTION_OUTPUT    = spec.mcp.redaction.output (JSON array)
          KAPETOOL_AUDIT_ENABLED       = spec.mcp.audit.enabled
          KAPETOOL_TASK_SERVICE_ENDPOINT = http://kape-task-service.{namespace}:8080
      - Mount settings.toml ConfigMap at /etc/kape/settings.toml
      - Inject platform secrets as env vars (LLM API key, NATS credentials)
      - Inject spec.envs entries verbatim
      - Pod annotation: kape.io/rollout-hash={rolloutHash}
      - automountServiceAccountToken: false
   b. Detect consumer name change vs existing ScaledObject
      → delete ScaledObject if changed (recreated in step 8)
   c. Create or patch Deployment (server-side apply)
   d. Read Deployment status → set DeploymentAvailable condition
  │
  ▼
8. RECONCILE KEDA SCALEDOBJECT
   Build with consumerName, lagThreshold, min/max replicas, cooldown
   Owner reference: KapeHandler → ScaledObject
   Create or patch (server-side apply)
   Read ScaledObject status → set ScalingConfigured condition
  │
  ▼
9. SYNC LABELS onto KapeHandler
   kape.io/schema-ref={spec.schemaRef}
   kape.io/tool-ref-{toolname}=true  (one per entry in spec.tools[])
  │
  ▼
10. COMPUTE state ROLLUP
    Pending  → DependenciesReady=False
    Active   → DependenciesReady=True AND DeploymentAvailable=True
    Degraded → DependenciesReady=True AND DeploymentAvailable=False
    Failed   → Degraded for > 10 minutes (via lastTransitionTime)
  │
  ▼
11. PATCH KapeHandler.status (status subresource)
  │
  ▼
12. RETURN
    Active/Degraded → RequeueAfter: 60s
    Pending         → RequeueAfter: 30s
    Failed          → RequeueAfter: 120s
```

### 6.2 settings.toml Structure

The operator renders a TOML file consumed by the handler runtime via dynaconf. The system prompt is inlined directly from `spec.llm.systemPrompt`. The schema is serialised directly from `KapeSchema.spec.jsonSchema` — no `$prompt` merge. Actions are serialised inline.

```toml
[kape]
handler_name          = "karpenter-consolidation-watcher"
handler_namespace     = "kape-system"
cluster_name          = "prod-apse1"
dry_run               = false           # from spec.dryRun
max_iterations        = 25              # from spec.llm.maxIterations
schema_name           = "karpenter-decision-schema"
replay_on_startup     = true            # from spec.trigger.replayOnStartup
max_event_age_seconds = 3600            # from spec.trigger.maxEventAgeSeconds

[llm]
provider      = "anthropic"
model         = "claude-sonnet-4-20250514"
system_prompt = """
  (operator inlines spec.llm.systemPrompt verbatim here)
"""

[nats]
subject  = "kape.events.karpenter.consolidation"
consumer = "kape-events-karpenter-consolidation"
stream   = "kape-events"

[task_service]
endpoint = "http://kape-task-service.kape-system:8080"

[otel]
endpoint     = "http://otel-collector.kape-system:4318"
service_name = "kape-handler"

# mcp-type tools — one section per entry, port assigned positionally
[tools.grafana-mcp]
type         = "mcp"
sidecar_port = 8080
transport    = "sse"

[tools.k8s-mcp-read]
type         = "mcp"
sidecar_port = 8081
transport    = "sse"

# memory-type tools — no sidecar, runtime connects to Qdrant directly
[tools.karpenter-memory]
type            = "memory"
qdrant_endpoint = "http://kape-memory-karpenter-memory.kape-system:6333"

# KapeSchema.spec.jsonSchema serialised directly — no $prompt merge
[schema]
json = """
{
  "type": "object",
  "required": ["decision", "confidence", "reasoning", "estimatedImpact"],
  "properties": {
    "decision":        { "type": "string", "enum": ["ignore", "investigate", "change-required"] },
    "confidence":      { "type": "number", "minimum": 0, "maximum": 1 },
    "reasoning":       { "type": "string", "minLength": 30 },
    "estimatedImpact": { "type": "string", "enum": ["low", "medium", "high", "critical"] }
  }
}
"""

# Actions serialised inline from spec.actions[]
[[actions]]
name      = "request-gitops-pr"
condition = "decision.decision == 'change-required'"
type      = "event-emitter"
[actions.data]
subject = "kape.events.gitops.pr-requested"
[actions.data.payload]
nodepool  = "{{ event.data.nodepool }}"
reasoning = "{{ decision.reasoning }}"
impact    = "{{ decision.estimatedImpact }}"

[[actions]]
name      = "notify-slack"
condition = "decision.decision == 'investigate' or decision.decision == 'change-required'"
type      = "webhook"
[actions.data]
url    = "{{ env.SLACK_WEBHOOK_URL }}"
method = "POST"
[actions.data.body]
text = "Karpenter alert on {{ event.data.nodepool }}: {{ decision.reasoning }}"

[[actions]]
name      = "store-investigation"
condition = "true"
type      = "save-memory"
[actions.data]
collection = "karpenter-investigations"
content    = "{{ decision.reasoning }}"
[actions.data.metadata]
nodepool = "{{ event.data.nodepool }}"
decision = "{{ decision.decision }}"
```

### 6.3 Sidecar Injection Details

The operator injects one `kapetool` sidecar container per `mcp`-type `KapeTool` in `spec.tools[]`. `memory`-type tools do not receive a sidecar — the handler runtime connects to their Qdrant endpoint directly using the `qdrant_endpoint` value in `settings.toml`.

Sidecar env vars are sourced from the `KapeTool` spec at reconcile time:

| Env var                          | Source                                                    |
| -------------------------------- | --------------------------------------------------------- |
| `KAPETOOL_UPSTREAM_URL`          | `spec.mcp.upstream.url`                                   |
| `KAPETOOL_UPSTREAM_TRANSPORT`    | `spec.mcp.upstream.transport`                             |
| `KAPETOOL_ALLOWED_TOOLS`         | `spec.mcp.allowedTools` (JSON array)                      |
| `KAPETOOL_REDACTION_INPUT`       | `spec.mcp.redaction.input` (JSON array, empty if absent)  |
| `KAPETOOL_REDACTION_OUTPUT`      | `spec.mcp.redaction.output` (JSON array, empty if absent) |
| `KAPETOOL_AUDIT_ENABLED`         | `spec.mcp.audit.enabled`                                  |
| `KAPETOOL_TASK_SERVICE_ENDPOINT` | `http://kape-task-service.{namespace}:8080` (fixed)       |

### 6.4 NATS Consumer Name Derivation

- Consumer name: `strings.ReplaceAll(spec.trigger.type, ".", "-")`
- Example: `kape.events.karpenter.consolidation` → `kape-events-karpenter-consolidation`
- Stream name: `kape-events` (fixed)
- NATS monitoring endpoint: `kape-config[nats.monitoringEndpoint]`, default `http://nats.kape-system:8222`

### 6.5 trigger.type Mutation Handling

If an engineer changes `spec.trigger.type` on a live handler, the derived consumer name changes. The operator detects this by comparing the consumer name in the existing ScaledObject against the freshly derived value. On mismatch: delete ScaledObject, recreate with new consumer name, annotate Deployment with new `rollout-hash` to force pod restart.

### 6.6 KEDA ScaledObject Generation

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: kape-handler-{name}
  namespace: {handler.namespace}
  ownerReferences:
    - apiVersion: kape.io/v1alpha1
      kind: KapeHandler
      name: {name}
      controller: true
      blockOwnerDeletion: true
spec:
  scaleTargetRef:
    name: kape-handler-{name}
  minReplicaCount: {spec.scaling.minReplicas}   # 0 if scaleToZero: true
  maxReplicaCount: {spec.scaling.maxReplicas}
  cooldownPeriod:  {spec.scaling.scaleDownStabilizationSeconds}
  triggers:
    - type: nats-jetstream
      metadata:
        natsServerMonitoringEndpoint: {kape-config[nats.monitoringEndpoint]}
        streamName: kape-events
        consumer: {consumerName}
        lagThreshold: "{spec.scaling.natsLagThreshold}"
```

Defaults when `spec.scaling` is absent: `minReplicas=1`, `maxReplicas=10`, `scaleToZero=false`, `natsLagThreshold=5`, `scaleDownStabilizationSeconds=60`.

Terminal validation failure: `scaleToZero: true` + `minReplicas >= 1` → set `ScalingConfigured=False`, reason `InvalidScalingConfig`. No Deployment or ScaledObject created until resolved.

### 6.7 KapeHandler Status Shape

```yaml
status:
  state: Pending | Active | Degraded | Failed
  conditions:
    - type: Ready
      status: "True" | "False"
      reason: DependenciesNotReady | DeploymentUnavailable | Ready
      message: "..."
    - type: DependenciesReady
      status: "True" | "False"
      reason: KapeToolNotReady | KapeSchemaInvalid | Ready
      message: "KapeTool grafana-mcp: MCPEndpointUnreachable"
    - type: DeploymentAvailable
      status: "True" | "False"
      reason: MinimumReplicasUnavailable | Available
      message: "..."
    - type: ScalingConfigured
      status: "True" | "False"
      reason: InvalidScalingConfig | KEDAScaledObjectNotReady | Configured
      message: "..."
  replicas: 2
  readyReplicas: 2
  observedGeneration: 4
```

Note: `SystemPromptMissing` reason removed from `DependenciesReady` — `systemPrompt` is inline in the CRD spec and cannot be missing independently.

---

## 7. Handler ServiceAccount

Each `KapeHandler` gets a dedicated ServiceAccount with zero Kubernetes RBAC permissions. The handler runtime never calls the Kubernetes API — the operator fully materialises all config before pod start.

### 7.1 ServiceAccount Spec

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kape-handler-{name}
  namespace: { handler.namespace }
  labels:
    kape.io/handler: { name }
  # Empty in v1 — reserved for cloud workload identity:
  # eks.amazonaws.com/role-arn: ...
  # iam.gke.io/service-account: ...
  ownerReferences:
    - apiVersion: kape.io/v1alpha1
      kind: KapeHandler
      name: { name }
      controller: true
      blockOwnerDeletion: true
```

### 7.2 Security Decisions

| Aspect          | Decision                                                                               |
| --------------- | -------------------------------------------------------------------------------------- |
| RBAC            | None — no Role, RoleBinding, or ClusterRole created                                    |
| Token mount     | `automountServiceAccountToken: false` on pod spec — set by operator, not on SA         |
| Cloud identity  | Empty annotations in v1, reserved for IRSA / GKE Workload Identity                     |
| k8s-mcp RBAC    | Belongs to the MCP server's own SA — engineer's responsibility                         |
| MCP access path | Handler → kapetool sidecar (localhost) → k8s-mcp server (has its own SA with K8s RBAC) |

---

## 8. Operator Deployment

### 8.1 Deployment Spec

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kape-operator
  namespace: kape-system
spec:
  replicas: 1 # scale to 2 for HA — leader election handles it
  template:
    spec:
      serviceAccountName: kape-operator
      automountServiceAccountToken: true # operator needs K8s API access
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532 # nonroot — distroless convention
        fsGroup: 65532
      containers:
        - name: kape-operator
          image: kape/operator:{version} # version from Helm chart
          args:
            - --leader-elect=true
            - --metrics-bind-address=:8080
            - --health-probe-bind-address=:8081
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: 500m
              memory: 256Mi
          securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          volumeMounts:
            - name: config
              mountPath: /etc/kape-operator
              readOnly: true
          livenessProbe:
            httpGet: { path: /healthz, port: 8081 }
            initialDelaySeconds: 15
            periodSeconds: 20
          readinessProbe:
            httpGet: { path: /readyz, port: 8081 }
            initialDelaySeconds: 5
            periodSeconds: 10
      volumes:
        - name: config
          configMap:
            name: kape-operator-config
      terminationGracePeriodSeconds: 10
```

### 8.2 Image

| Aspect     | Decision                                                       |
| ---------- | -------------------------------------------------------------- |
| Base image | `gcr.io/distroless/static` — no shell, smallest attack surface |
| Build      | `CGO_ENABLED=0 GOOS=linux go build` — pure static binary       |
| Version    | Set via Helm chart value `operator.image.tag`                  |

---

## 9. Operator RBAC

### 9.1 ClusterRole: kape-operator

```yaml
rules:
  # KAPE CRDs
  - apiGroups: ["kape.io"]
    resources: [kapehandlers, kapetools, kapeschemas]
    verbs: [get, list, watch, update, patch]
  - apiGroups: ["kape.io"]
    resources:
      - kapehandlers/status
      - kapetools/status
      - kapeschemas/status
      - kapehandlers/finalizers
      - kapeschemas/finalizers
    verbs: [get, update, patch]

  # ConfigMaps — write rendered settings.toml, read kape-config
  # Note: no longer needs to read systemPromptRef ConfigMaps — systemPrompt is inline in CRD
  - apiGroups: [""]
    resources: [configmaps]
    verbs: [get, list, watch, create, update, patch, delete]

  # ServiceAccounts — per-handler SA
  - apiGroups: [""]
    resources: [serviceaccounts]
    verbs: [get, list, watch, create, update, patch, delete]

  # Secrets — existence check only, never reads content
  - apiGroups: [""]
    resources: [secrets]
    verbs: [get, list, watch]

  # Deployments — handler pods
  - apiGroups: [apps]
    resources: [deployments]
    verbs: [get, list, watch, create, update, patch, delete]

  # StatefulSets — Qdrant instances
  - apiGroups: [apps]
    resources: [statefulsets]
    verbs: [get, list, watch, create, update, patch, delete]

  # Services — headless service per Qdrant instance
  - apiGroups: [""]
    resources: [services]
    verbs: [get, list, watch, create, update, patch, delete]

  # PVCs — Qdrant storage
  - apiGroups: [""]
    resources: [persistentvolumeclaims]
    verbs: [get, list, watch, create, delete]

  # KEDA ScaledObjects
  - apiGroups: [keda.sh]
    resources: [scaledobjects]
    verbs: [get, list, watch, create, update, patch, delete]

  # Events
  - apiGroups: [""]
    resources: [events]
    verbs: [create, patch]
```

### 9.2 Role: kape-operator-leader-election (kape-system only)

```yaml
rules:
  - apiGroups: [coordination.k8s.io]
    resources: [leases]
    verbs: [get, list, watch, create, update, patch, delete]
```

### 9.3 Kubernetes Events Emitted

| Resource      | Reason                   | Type    | Message                                                            |
| ------------- | ------------------------ | ------- | ------------------------------------------------------------------ |
| `KapeHandler` | `DependencyNotReady`     | Warning | `KapeTool grafana-mcp not ready: MCPEndpointUnreachable`           |
| `KapeHandler` | `DeploymentCreated`      | Normal  | `Created Deployment kape-handler-{name}`                           |
| `KapeHandler` | `DeploymentRolled`       | Normal  | `Rolled Deployment: rollout-hash changed`                          |
| `KapeHandler` | `HandlerActive`          | Normal  | `Handler is Active — all dependencies ready, deployment available` |
| `KapeHandler` | `HandlerDegraded`        | Warning | `Handler is Degraded — deployment unavailable for 2m`              |
| `KapeHandler` | `InvalidScalingConfig`   | Warning | `scaleToZero: true requires minReplicas: 0`                        |
| `KapeTool`    | `QdrantProvisioned`      | Normal  | `StatefulSet kape-memory-{name} created`                           |
| `KapeTool`    | `MCPEndpointUnreachable` | Warning | `Health probe failed: GET {url} → {status}`                        |
| `KapeSchema`  | `DeletionBlocked`        | Warning | `Cannot delete: referenced by handlers: [handler-a, handler-b]`    |
| `KapeSchema`  | `SchemaValid`            | Normal  | `JSON Schema validated successfully`                               |

---

## 10. Leader Election

| Aspect           | Decision                                         |
| ---------------- | ------------------------------------------------ |
| Mechanism        | controller-runtime built-in via Kubernetes Lease |
| Lease name       | `kape-operator-leader-election`                  |
| Lease namespace  | `kape-system`                                    |
| Renewal interval | 15s (controller-runtime default)                 |
| Retry period     | 10s (controller-runtime default)                 |
| Default replicas | 1 — scale to 2 for HA, no code change required   |
| Flag             | `--leader-elect=true` (default, configurable)    |

---

## 11. Prometheus Metrics

### 11.1 Built-in (controller-runtime)

- `controller_runtime_reconcile_total{controller, result}`
- `controller_runtime_reconcile_errors_total{controller}`
- `controller_runtime_reconcile_time_seconds{controller}`
- `controller_runtime_active_workers{controller}`
- `workqueue_depth{name}`
- `workqueue_queue_duration_seconds{name}`

### 11.2 Custom Metrics

| Metric                                                            | Description                                                          |
| ----------------------------------------------------------------- | -------------------------------------------------------------------- |
| `kape_handlers_total{namespace, state}`                           | Gauge — handler count by state (Pending/Active/Degraded/Failed)      |
| `kape_tools_total{namespace, type, ready}`                        | Gauge — KapeTool count by type and readiness                         |
| `kape_qdrant_instances_total{namespace, ready}`                   | Gauge — Qdrant StatefulSet count and readiness                       |
| `kape_dependency_gate_failures_total{namespace, handler, reason}` | Counter — hard gate failures, useful for alerting on flapping deps   |
| `kape_handler_activation_duration_seconds{namespace}`             | Histogram — time from KapeHandler creation to Active state           |
| `kape_scaledobject_recreations_total{namespace, handler}`         | Counter — ScaledObject recreations triggered by trigger.type changes |

### 11.3 PrometheusRule Alerts (Helm chart)

```yaml
- alert: KapeHandlerStuckPending
  expr: kape_handlers_total{state="Pending"} > 0
  for: 5m
  annotations:
    summary: "KapeHandler stuck in Pending state for > 5 minutes"

- alert: KapeOperatorReconcileErrors
  expr: rate(controller_runtime_reconcile_errors_total[5m]) > 0.1
  annotations:
    summary: "Kape operator reconcile error rate elevated"
```

---

## 12. Error Handling and Requeue Strategy

### 12.1 Error Categories

| Category                                              | Return value                         | Behaviour                                                                     |
| ----------------------------------------------------- | ------------------------------------ | ----------------------------------------------------------------------------- |
| Transient — unexpected API failure, network blip      | `ctrl.Result{}, err`                 | controller-runtime exponential backoff (5ms base, 1000s max)                  |
| Expected wait — dependency not ready, Qdrant starting | `ctrl.Result{RequeueAfter: Ns}, nil` | Predictable polling, no backoff                                               |
| Terminal — invalid spec, config contradiction         | `ctrl.Result{}, nil`                 | Set status condition, emit Event, no requeue. Triggered again on spec change. |

### 12.2 Requeue Intervals

| Condition                              | RequeueAfter                    |
| -------------------------------------- | ------------------------------- |
| Dependency not ready (hard gate)       | 30s                             |
| Qdrant StatefulSet not ready           | 15s                             |
| MCP endpoint health refresh            | 30s                             |
| Handler Active (periodic health check) | 60s                             |
| Handler Degraded                       | 60s                             |
| Handler Failed                         | 120s                            |
| Invalid spec (terminal)                | No requeue                      |
| Unexpected error                       | Exponential backoff (automatic) |

### 12.3 Concurrency

| Aspect                    | Decision                                                                             |
| ------------------------- | ------------------------------------------------------------------------------------ |
| `MaxConcurrentReconciles` | 3 per controller (default)                                                           |
| Configurable via          | `--max-concurrent-reconciles` flag and `operator.maxConcurrentReconciles` Helm value |
| Applied to                | All three controllers uniformly                                                      |
| Jitter                    | None — fixed intervals for v1                                                        |

---

## 13. kape-config ConfigMap Reference

Single ConfigMap in `kape-system`. All component versions and infrastructure endpoints are centralised here. Updated at upgrade time — operator reads on every reconcile.

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

  # KapeTool sidecar
  kapetool.image: "kape/kapetool"
  kapetool.version: "v0.1.0"

  # Handler runtime
  kapehandler.image: "kape/handler"
  kapehandler.version: "v0.1.0"

  # NATS
  nats.monitoringEndpoint: "http://nats.kape-system:8222"

  # Handler defaults (overridable per KapeHandler via spec.llm.maxIterations)
  handler.maxIterations: "50"
```

---

## 14. Decision Registry

All decisions locked for the operator design. Treat as authoritative for implementation.

### Scope

| Decision                 | Value                                                                       |
| ------------------------ | --------------------------------------------------------------------------- |
| Operator namespace scope | Cluster-scoped — watches all namespaces, `kape-system` examples only for v1 |

### KapeToolReconciler

| Decision                     | Value                                                                                                      |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------- |
| type:mcp health probe        | HTTP GET to `spec.mcp.upstream.url + /health` (configurable), 5s timeout, `RequeueAfter: 30s`              |
| type:mcp sidecar injection   | Operator reads `upstream`, `allowedTools`, `redaction`, `audit` from KapeTool spec and injects as env vars |
| type:memory Qdrant lifecycle | One StatefulSet per KapeTool. Owner reference cascade. PV orphaned on delete.                              |
| type:memory future upgrade   | Swap StatefulSet for `QdrantCluster` CRD when qdrant-operator matures                                      |
| type:event-publish           | Validation only. No sidecar. Not part of hard gate.                                                        |
| Qdrant collection name       | `default` (one collection per instance)                                                                    |
| StatefulSet name             | `kape-memory-{name}`                                                                                       |
| Storage size                 | `spec.memory.storage` (default `10Gi`)                                                                     |
| Qdrant version               | `kape-config[qdrant.version]`                                                                              |
| Qdrant endpoint              | Written to `status.qdrantEndpoint`, serialised into `settings.toml [tools.{name}]`                         |

### KapeSchemaReconciler

| Decision                | Value                                                                                          |
| ----------------------- | ---------------------------------------------------------------------------------------------- |
| Deletion protection     | Finalizer `kape.io/schema-protection` blocks delete if handlers reference schema               |
| Handler discovery       | Label index: `kape.io/schema-ref={name}` on `KapeHandler`                                      |
| Handler rollout trigger | `status.schemaHash` change → `KapeHandlerReconciler` re-enqueues, updates `rollout-hash`       |
| Schema serialisation    | Operator serialises `spec.jsonSchema` directly — no `$prompt` merge (removed in CRD RFC rev 4) |
| Validation              | No longer checks for `$prompt` keys — removed from validator                                   |

### KapeHandlerReconciler

| Decision                  | Value                                                                                                                                                                                                 |
| ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Dependency gating         | Hard gate on `KapeSchema` + all `KapeTools` in `spec.tools[]` only. `spec.actions[]` are inline — no KapeTool gate.                                                                                   |
| `systemPromptRef`         | Removed. `systemPrompt` is inline in `spec.llm.systemPrompt`. No ConfigMap watch.                                                                                                                     |
| Config delivery           | Operator-rendered `settings.toml` (TOML/dynaconf) + secrets as env vars                                                                                                                               |
| Sidecar injection         | One `kapetool` sidecar per `mcp`-type tool in `spec.tools[]`. `memory`-type tools: no sidecar, Qdrant endpoint in `settings.toml`.                                                                    |
| Sidecar env vars          | `KAPETOOL_UPSTREAM_URL`, `KAPETOOL_UPSTREAM_TRANSPORT`, `KAPETOOL_ALLOWED_TOOLS`, `KAPETOOL_REDACTION_INPUT`, `KAPETOOL_REDACTION_OUTPUT`, `KAPETOOL_AUDIT_ENABLED`, `KAPETOOL_TASK_SERVICE_ENDPOINT` |
| Rollout hash inputs       | `KapeHandler.spec` + `KapeSchema.spec` + all `KapeTool.spec` in `spec.tools[]`. No ConfigMap input.                                                                                                   |
| trigger.type mutation     | Allow — delete/recreate ScaledObject, Deployment rolls automatically                                                                                                                                  |
| NATS consumer name        | `strings.ReplaceAll(trigger.type, ".", "-")`                                                                                                                                                          |
| NATS stream name          | `kape-events` (fixed)                                                                                                                                                                                 |
| KEDA defaults             | `min=1`, `max=10`, `scaleToZero=false`, `lagThreshold=5`, `cooldown=60s`                                                                                                                              |
| ScaledObject edge case    | `scaleToZero=true` + `minReplicas>=1` → terminal `InvalidScalingConfig`                                                                                                                               |
| Runtime metrics in status | None — dashboard reads `kape-task-service` directly                                                                                                                                                   |

### Handler ServiceAccount

| Decision       | Value                                                              |
| -------------- | ------------------------------------------------------------------ |
| RBAC           | Zero — no Role, RoleBinding, or ClusterRole                        |
| Token mount    | `automountServiceAccountToken: false` on pod spec                  |
| Cloud identity | Empty annotations in v1, reserved for IRSA / GKE Workload Identity |

### Go Architecture

| Decision            | Value                                                                  |
| ------------------- | ---------------------------------------------------------------------- |
| DDD structure       | Flat three layers: `domain`, `infra`, `controller`                     |
| Module layout       | One Go module per layer + `cmd`                                        |
| Ports location      | `infra/ports/` — interfaces owned by infra boundary                    |
| CRD Go types        | `infra/api/v1alpha1/` — Kubernetes types belong in infra               |
| Domain structs      | Pure Go, zero Kubernetes imports, mapped from CRD types by infra repos |
| Removed from domain | `handler/schema_merger.go` — `$prompt` merge removed in CRD RFC rev 4  |
| Config library      | `ff` v3 with `ffyaml.Parser`                                           |
| Config priority     | CLI flags → `KAPE_OPERATOR_*` env vars → `config.yaml` → defaults      |
| Config file format  | YAML at `/etc/kape-operator/config.yaml`                               |
| Env var prefix      | `KAPE_OPERATOR_`                                                       |

### Operator Deployment

| Decision         | Value                                                                                  |
| ---------------- | -------------------------------------------------------------------------------------- |
| Base image       | `gcr.io/distroless/static`                                                             |
| Resources        | `100m`/`128Mi` requests, `500m`/`256Mi` limits                                         |
| Security context | `runAsNonRoot`, `runAsUser=65532`, `readOnlyRootFilesystem`, `capabilities: drop: ALL` |
| Default replicas | 1 — scale to 2 for HA, leader election handles it                                      |

### Leader Election

| Decision | Value                                            |
| -------- | ------------------------------------------------ |
| Enabled  | Always — flag configurable, default `true`       |
| Lease    | `kape-operator-leader-election` in `kape-system` |

### Metrics and Error Handling

| Decision                  | Value                                                    |
| ------------------------- | -------------------------------------------------------- |
| Custom metrics            | 6 (see section 11.2)                                     |
| PrometheusRule alerts     | `KapeHandlerStuckPending`, `KapeOperatorReconcileErrors` |
| `MaxConcurrentReconciles` | 3 per controller, configurable via flag and Helm         |
| Jitter                    | None for v1                                              |
| Kubernetes Events         | Yes — 10 defined reasons across all three CRD types      |
