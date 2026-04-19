# Phase 6 — Full Operator Design

**Date:** 2026-04-19
**Author:** Dzung Tran
**Depends on:** specs/0002-crds-design, specs/0005-kape-operator, specs/0012-v1-roadmap (Phase 6)
**Status:** Approved

---

## Overview

Phase 6 completes the KAPE operator. It adds two new reconcilers (`KapeToolReconciler`, `KapeSchemaReconciler`) and replaces the minimal Phase 2 `KapeHandlerReconciler` with the full 12-step implementation including dependency gating, sidecar injection, and KEDA ScaledObject generation.

**Decisions locked in this session:**

- KEDA ScaledObject is created via `unstructured.Unstructured` — no `kedacore/keda/v2` Go module dependency
- Qdrant collection creation is **not** done by the operator — the operator provisions StatefulSet + Service only; the runtime (LangChain `QdrantVectorStore`) creates the collection on first use
- `KapeHandlerReconciler` is a full rewrite (not an extension of the Phase 2 version)

---

## 1. Architecture

Three reconcilers fully wired. Flat single Go module — no module split.

| Reconciler | Status | Manages |
|---|---|---|
| `KapeToolReconciler` | New | Qdrant StatefulSet + Service (memory), MCP health probe (mcp), validation (event-publish) |
| `KapeSchemaReconciler` | New | JSON Schema validation, deletion-protection finalizer, `status.schemaHash` |
| `KapeHandlerReconciler` | Replaces Phase 2 | Full 12-step flow: dependency gate → ConfigMap → SA → Deployment+sidecars → KEDA → status |

---

## 2. New and Modified Files

### 2.1 Infra — Types

| File | Change |
|---|---|
| `infra/api/v1alpha1/kapetool_types.go` | Add `QdrantEndpoint string` to `KapeToolStatus` |
| `infra/api/v1alpha1/kapeschema_types.go` | Add `SchemaHash string` to `KapeSchemaStatus` |

### 2.2 Infra — Ports

| File | Interfaces |
|---|---|
| `infra/ports/tool.go` | `ToolRepository` (Get, UpdateStatus, ListHandlersByToolRef), `StatefulSetPort`, `ScaledObjectPort` |
| `infra/ports/schema.go` | `SchemaRepository` |

### 2.3 Infra — Adapters

| File | Implements |
|---|---|
| `infra/k8s/tool_repo.go` | `ToolRepository` — Get, UpdateStatus, ListByHandlerRef |
| `infra/k8s/schema_repo.go` | `SchemaRepository` — Get, UpdateStatus, AddFinalizer, RemoveFinalizer, ListHandlersBySchemaRef |
| `infra/k8s/statefulset.go` | `StatefulSetPort` — Qdrant StatefulSet + headless Service create/patch/status |
| `infra/k8s/scaledobject.go` | `ScaledObjectPort` — unstructured ScaledObject create/patch/delete/get |

### 2.4 Controller

| File | Purpose |
|---|---|
| `controller/tool.go` | Thin `KapeToolReconciler` + `SetupToolReconciler` with Owns watches |
| `controller/schema.go` | Thin `KapeSchemaReconciler` + `SetupSchemaReconciler` |
| `controller/reconcile/tool.go` | Full KapeTool reconcile logic |
| `controller/reconcile/schema.go` | Full KapeSchema reconcile logic |
| `controller/reconcile/handler.go` | **Replaced** — full 12-step handler reconcile |
| `controller/watches.go` | Secondary watch map functions |
| `controller/indexer.go` | Label field index registration |
| `cmd/main.go` | Updated — wire all three reconcilers |

---

## 3. KapeToolReconciler

### 3.1 Dispatch

```
Reconcile(KapeTool)
  ├── type: memory        → reconcileMemory()
  ├── type: mcp           → reconcileMCP()
  └── type: event-publish → reconcileEventPublish()
```

### 3.2 type: memory

```
1. Ensure StatefulSet kape-memory-{name}
   Image:        qdrant/qdrant:{kape-config[qdrant.version]}
   Storage:      10Gi (hardcoded — MemorySpec has no storage field in v1 CRD)
   StorageClass: kape-config[qdrant.storageClass]
   OwnerRef:     KapeTool → StatefulSet (cascade delete)

2. Ensure headless Service kape-memory-{name}
   Port 6333 (HTTP), 6334 (gRPC)
   OwnerRef: KapeTool → Service

3. Check StatefulSet.Status.ReadyReplicas >= 1
   → False: Ready=False, reason=QdrantNotReady, RequeueAfter: 15s
   → True:  Ready=True
            Write status.qdrantEndpoint = http://kape-memory-{name}.{namespace}:6333
```

No Qdrant HTTP API call. Collection creation is deferred to the handler runtime.

### 3.3 type: mcp

```
1. HTTP GET spec.mcp.upstream.url + "/health"
   Timeout: 5s, retries: 3
   → Unreachable: Ready=False, reason=MCPEndpointUnreachable, message=url+status
   → Reachable:   Ready=True
2. RequeueAfter: 30s
```

### 3.4 type: event-publish

```
1. Validate spec.eventPublish.type starts with "kape.events."
   → Invalid: Ready=False, reason=ValidationFailed (terminal)
   → Valid:   Ready=True
No RequeueAfter.
```

### 3.5 KapeToolStatus Addition

```go
// QdrantEndpoint is the Qdrant HTTP endpoint for memory-type tools.
// Written after StatefulSet reaches ReadyReplicas >= 1.
// +optional
QdrantEndpoint string `json:"qdrantEndpoint,omitempty"`
```

---

## 4. KapeSchemaReconciler

### 4.1 Reconcile Flow

```
1. Validate spec.jsonSchema
   - Root type must be "object"
   - All required[] entries must exist in properties
   → Invalid: Ready=False, reason=InvalidSchema (terminal)

2. Manage finalizer kape.io/schema-protection
   - Add on first reconcile if absent

3. Handle deletion (DeletionTimestamp set)
   - List KapeHandlers with label kape.io/schema-ref={name}
   - If references exist → block, emit Warning event, return
   - If none → remove finalizer

4. Compute schemaHash = sha256(spec.version + marshalled spec.jsonSchema)
   Write to status.schemaHash

5. Set Ready=True, reason=Valid
```

### 4.2 KapeSchemaStatus Addition

```go
// SchemaHash is a sha256 of spec.version + spec.jsonSchema.
// Changes trigger rollout of all referencing KapeHandlers via secondary watch.
// +optional
SchemaHash string `json:"schemaHash,omitempty"`
```

### 4.3 Handler Rollout Signalling

Secondary watch on `KapeSchema` in `controller/schema.go`: on `status.schemaHash` change, map function lists `KapeHandlers` with label `kape.io/schema-ref={name}` and re-enqueues each. The handler reconciler recomputes `rolloutHash` (which includes `KapeSchema.spec`), detects the change, updates the pod annotation, and triggers a Deployment rollout.

---

## 5. KapeHandlerReconciler (Full Replacement)

### 5.1 Dependencies Added vs Phase 2

Phase 2 had: `HandlerRepository`, `ConfigMapPort`, `ServiceAccountPort`, `DeploymentPort`, `TOMLRenderer`, `KapeConfigLoader`

Phase 6 adds: `ToolRepository`, `SchemaRepository`, `ScaledObjectPort`

### 5.2 Full 12-Step Reconcile Flow

```
1. FETCH
   Return if not found (GC handles owned resources)

2. DEPENDENCY GATE (hard gate)
   a. KapeSchema[spec.schemaRef] exists AND Ready=True
      → False: DependenciesReady=False, reason=KapeSchemaInvalid, RequeueAfter: 30s
   b. foreach tool in spec.tools[]:
        KapeTool exists AND Ready=True
        → False: DependenciesReady=False, reason=KapeToolNotReady,
                 message="KapeTool {name}: {tool.status.conditions[Ready].message}"
                 RequeueAfter: 30s
   All pass → DependenciesReady=True, continue

3. VALIDATE SCALING
   scaleToZero=true AND minReplicas >= 1
   → ScalingConfigured=False, reason=InvalidScalingConfig (terminal — no requeue)

4. COMPUTE HASHES
   rolloutHash = sha256(KapeHandler.spec + KapeSchema.spec + all KapeTool.spec in tools[])
   consumerName = strings.ReplaceAll(spec.trigger.type, ".", "-")

5. RENDER settings.toml
   - Inline spec.llm.systemPrompt
   - mcp tools: sidecar_port assigned positionally (8080, 8081, ...)
   - memory tools: qdrant_endpoint from KapeTool.status.qdrantEndpoint
   - Schema JSON from KapeSchema.spec.jsonSchema (no $prompt merge)
   - Actions inline from spec.actions[]
   Write ConfigMap kape-handler-{name}

6. ENSURE ServiceAccount kape-handler-{name}

7. ENSURE Deployment
   - handler container (image from kape-config)
   - One kapetool sidecar per mcp-type KapeTool in spec.tools[]
     Sidecar env vars per tool:
       KAPETOOL_UPSTREAM_URL          = spec.mcp.upstream.url
       KAPETOOL_UPSTREAM_TRANSPORT    = spec.mcp.upstream.transport
       KAPETOOL_ALLOWED_TOOLS         = spec.mcp.allowedTools (JSON array)
       KAPETOOL_REDACTION_INPUT       = spec.mcp.redaction.input (JSON array)
       KAPETOOL_REDACTION_OUTPUT      = spec.mcp.redaction.output (JSON array)
       KAPETOOL_AUDIT_ENABLED         = spec.mcp.audit.enabled
       KAPETOOL_TASK_SERVICE_ENDPOINT = http://kape-task-service.{namespace}:8080
   - settings.toml mounted at /etc/kape/settings.toml
   - spec.envs injected verbatim
   - pod annotation kape.io/rollout-hash={rolloutHash}
   - automountServiceAccountToken: false

8. ENSURE KEDA ScaledObject (unstructured)
   - Detect consumer name change: compare consumerName vs existing ScaledObject
     spec.triggers[0].metadata.consumer → if changed, delete existing, recreate
   - NatsJetStreamScaler, consumerName, lagThreshold from spec.scaling
   - Defaults: min=1, max=10, scaleToZero=false, lagThreshold=5, cooldown=60s
   - ownerRef → KapeHandler

9. SYNC LABELS onto KapeHandler
   kape.io/schema-ref={spec.schemaRef}
   kape.io/tool-ref-{name}=true  (one per spec.tools[] entry)

10. READ Deployment status → DeploymentAvailable condition

11. COMPUTE state rollup
    Active   → DependenciesReady=True AND DeploymentAvailable=True
    Degraded → DependenciesReady=True AND DeploymentAvailable=False
    Pending  → DependenciesReady=False

12. PATCH KapeHandler.status
    Active/Degraded → RequeueAfter: 60s
    Pending         → RequeueAfter: 30s
    Terminal error  → no requeue
```

---

## 6. Cross-Resource Watches

Defined in `controller/watches.go`, registered in `SetupHandlerReconciler`.

| Watch target | Trigger condition | Map function |
|---|---|---|
| `KapeTool` | Any spec or status change | List KapeHandlers with label `kape.io/tool-ref-{name}=true`, re-enqueue each |
| `KapeSchema` | `status.schemaHash` change | List KapeHandlers with label `kape.io/schema-ref={name}`, re-enqueue each |

Label field indexes registered in `controller/indexer.go` on manager start for O(1) lookups.

---

## 7. KEDA ScaledObject (Unstructured)

No `kedacore/keda/v2` import. The ScaledObject is constructed as `unstructured.Unstructured`:

```go
obj := &unstructured.Unstructured{
    Object: map[string]interface{}{
        "apiVersion": "keda.sh/v1alpha1",
        "kind":       "ScaledObject",
        "metadata": map[string]interface{}{
            "name":      "kape-handler-" + handler.Name,
            "namespace": handler.Namespace,
        },
        "spec": map[string]interface{}{
            "scaleTargetRef": map[string]interface{}{"name": "kape-handler-" + handler.Name},
            "minReplicaCount": minReplicas,
            "maxReplicaCount": maxReplicas,
            "cooldownPeriod":  cooldown,
            "triggers": []interface{}{
                map[string]interface{}{
                    "type": "nats-jetstream",
                    "metadata": map[string]interface{}{
                        "natsServerMonitoringEndpoint": natsMonitoringEndpoint,
                        "streamName":    "kape-events",
                        "consumer":      consumerName,
                        "lagThreshold":  lagThreshold,
                    },
                },
            },
        },
    },
}
```

KEDA CRD must be installed on the cluster. No scheme registration is needed — `client.Client` in controller-runtime handles `unstructured.Unstructured` natively without any GVK registered in the manager scheme.

---

## 8. settings.toml Renderer Updates

The existing `TOMLRenderer` is updated to include:

- `[tools.{name}]` section per tool in `spec.tools[]`:
  - `mcp` type: `type = "mcp"`, `sidecar_port = {positional}`, `transport = {upstream.transport}`
  - `memory` type: `type = "memory"`, `qdrant_endpoint = {status.qdrantEndpoint}`
- `[schema]` section: `json = "{escaped KapeSchema.spec.jsonSchema}"`
- `[[actions]]` sections: inline from `spec.actions[]`

The renderer signature changes to accept `KapeSchema` and `[]KapeTool` alongside `KapeHandler` and `KapeConfig`:

```go
Render(handler *v1alpha1.KapeHandler, schema *v1alpha1.KapeSchema, tools []v1alpha1.KapeTool, cfg domainconfig.KapeConfig) (string, error)
```

---

## 9. Acceptance Criteria

All from spec 0012 Phase 6:

1. Apply KapeHandler + KapeTool (memory type) → Qdrant StatefulSet appears; handler Deployment has `QDRANT_*` env vars
2. Apply KapeTool (mcp type) → sidecar injected into handler Deployment
3. Apply KapeSchema → `kubectl get kapeschema` shows `status.conditions[Ready]=True`
4. Attempt to delete KapeSchema referenced by KapeHandler → deletion blocked with clear error
5. KEDA ScaledObject visible; `kubectl get scaledobject` shows correct min/max replicas

---

## 10. Out of Scope

- Qdrant collection creation (deferred to runtime Phase 7)
- `KapeTool` sidecar image (`kapetool`) implementation — the sidecar container is injected by the operator but the image itself is not built in Phase 6
- PrometheusRule alerts (Helm chart, Phase 10)
- mTLS for NATS (Phase 8)
