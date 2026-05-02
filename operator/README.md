# KAPE Operator

The KAPE operator reconciles three custom resources — `KapeHandler`, `KapeTool`, `KapeSchema` — into the Kubernetes objects that run a KAPE agent: a `Deployment`, a `ConfigMap` holding `settings.toml`, and a per-handler `ServiceAccount`. It is a controller-runtime operator written in Go.

This README has two jobs:

1. Document **how the code is structured** so contributors can navigate it.
2. Serve as a **teaching aid** for readers who know Go but have not written a Kubernetes operator before. The narrative explains *why* the code is laid out this way, not just *what* lives where.

---

## What the operator does

In one sentence: when a user `kubectl apply`s a `KapeHandler`, the operator turns it into a running pod that subscribes to a NATS subject and runs an LLM ReAct loop.

In a few more sentences:

- A user declares an agent with `kind: KapeHandler` (event source, LLM provider/model, tool refs, schema ref, post-decision actions).
- The operator watches `KapeHandler` resources and, for each one, ensures a matching `ConfigMap` (`settings.toml`), `ServiceAccount`, and `Deployment` exist, with the correct content, labels, owner references, and a rollout-hash annotation that triggers a pod restart whenever the spec changes.
- The operator also reads cluster-wide defaults from a `kape-config` `ConfigMap` in `kape-system` (image refs, NATS endpoint, default max iterations) so a fresh cluster works without per-handler boilerplate.
- The operator's `KapeHandler.status` reflects Deployment readiness via `Ready` and `DeploymentAvailable` conditions, plus a `replicas` count.

What it currently does **not** do (scaffolded but not implemented — see *Empty scaffolds* below): no admission webhook, no liveness/readiness probe builders for the handler pod, no operator-side metrics beyond what controller-runtime publishes by default.

The CRDs the operator owns are defined in `operator/infra/api/v1alpha1/`:

- `KapeHandler` — `operator/infra/api/v1alpha1/kapehandler_types.go:222` — primary CRD; one handler = one agent pipeline.
- `KapeTool` — `operator/infra/api/v1alpha1/kapetool_types.go:143` — declares an MCP, memory, or event-publish tool capability.
- `KapeSchema` — `operator/infra/api/v1alpha1/kapeschema_types.go:58` — the JSON Schema contract for the LLM's structured output.

---

## Operator 101 (read this if you're new to operators)

Skip this section if you've written a controller-runtime operator before.

A **Kubernetes operator** is a long-running pod that watches one or more resource types and tries to make the cluster's actual state match each resource's `spec`. The mental model is a control loop:

```
observe → diff → act → update status → wait → observe → ...
```

The community library that does the plumbing is [controller-runtime](https://pkg.go.dev/sigs.k8s.io/controller-runtime). It gives you:

- A **Manager** that hosts shared infrastructure: a cached client, leader election, the metrics server, health probes, and a signal handler for graceful shutdown.
- A **Reconciler** interface — `Reconcile(ctx, req) (Result, error)` — which you implement. The framework calls it whenever a watched resource changes (or when you ask it to requeue).
- **Owner references** — when object A owns object B, deleting A garbage-collects B. The operator uses this so deleting a `KapeHandler` cleans up the matching Deployment / ConfigMap / ServiceAccount automatically. No finalizer needed for that case.
- **Watches** — `For(...)` watches the primary type; `Owns(...)` watches owned children and re-enqueues the parent on child changes (so if someone manually edits the Deployment, the operator reverts it).

Two important properties you should bake into every reconciler:

- **Idempotent.** Reconcile may run many times for the same object. Each run must converge to the same end state. The KAPE reconciler does this by always computing the desired object from the spec and patching the existing one (or creating if absent).
- **Level-triggered, not edge-triggered.** Reconcile receives a *namespaced name*, not a diff. You always re-fetch and re-derive from the current spec. If you miss an event, the next reconcile catches up. The KAPE reconciler also schedules a periodic requeue (`RequeueAfter: 60s`) as a backstop.

Useful upstream reading:

- Kubebuilder book: <https://book.kubebuilder.io/>
- controller-runtime godoc: <https://pkg.go.dev/sigs.k8s.io/controller-runtime>
- "Anatomy of a Kubernetes operator" — <https://kubernetes.io/docs/concepts/extend-kubernetes/operator/>

---

## Package layout

The operator follows a **hexagonal (ports & adapters)** split. The reconcile logic depends only on Go interfaces (ports); concrete Kubernetes calls live behind those interfaces (adapters). This is unusual for a controller-runtime project — most projects let `client.Client` leak everywhere — and the rest of this README explains why we picked it.

```
operator/
├── cmd/                    # process entry point — wiring only
│   └── main.go
├── domain/                 # pure Go, no k8s imports
│   └── config/             #   KapeConfig value object + defaults
├── controller/             # controller-runtime adapters (thin)
│   ├── handler.go          #   KapeHandlerReconciler + SetupHandlerReconciler
│   ├── schema.go           #   KapeSchemaReconciler + SetupSchemaReconciler
│   ├── tool.go             #   KapeToolReconciler + SetupToolReconciler
│   ├── watches.go          #   MapToolToHandlers / MapSchemaToHandlers (cross-watch)
│   └── reconcile/
│       ├── handler.go      #   12-step KapeHandler reconcile algorithm
│       ├── schema.go       #   KapeSchema reconcile algorithm (validate + finalizer)
│       └── tool.go         #   KapeTool reconcile algorithm (memory/mcp/event-publish)
└── infra/                  # everything that touches Kubernetes or external systems
    ├── api/v1alpha1/       #   CRD Go types + zz_generated.deepcopy.go
    ├── ports/              #   outbound interfaces the reconcilers depend on
    ├── k8s/                #   adapters that satisfy ports using sigs.k8s.io/client
    └── toml/               #   adapter that renders settings.toml
```

| Directory                | Role                                   | Depends on                              |
|--------------------------|----------------------------------------|-----------------------------------------|
| `cmd/`                   | Parse flags, build manager, wire deps. | Everything below.                       |
| `domain/`                | Plain values and rules. No k8s.        | Nothing in this module.                 |
| `controller/`            | controller-runtime glue.               | `controller/reconcile`, `infra/api`.    |
| `controller/reconcile/`  | Reconcile algorithms (all three CRDs). | `infra/ports`, `infra/api`, `domain`.   |
| `infra/api/v1alpha1/`    | CRD Go types (kubebuilder tags).       | k8s apimachinery.                       |
| `infra/ports/`           | Outbound interfaces.                   | `domain`, `infra/api` (data shapes).    |
| `infra/k8s/`             | Adapters using `client.Client`.        | k8s client, `infra/api`, `domain`.      |
| `infra/toml/`            | `settings.toml` renderer.              | go-toml, `infra/api`, `domain`.         |

### Why this split

A naive controller-runtime reconciler embeds a `client.Client` and calls `r.Get(...)`, `r.Create(...)`, `r.Patch(...)` directly inside `Reconcile`. That works, but it makes two things painful:

- **Tests** need either envtest (a real apiserver in a binary) or a complex fake client. Both are slow to set up and slow to run.
- **Swapping infra** (e.g. rendering Helm output instead of writing to the apiserver, or adding a second backend) means rewriting the reconciler.

By keeping the reconciler dependent only on small, purpose-built interfaces (`HandlerRepository`, `ConfigMapPort`, `DeploymentPort`, ...), each one becomes trivially mockable with a hand-written fake. The reconciler can be unit-tested without any Kubernetes machinery; the adapters can be tested separately against envtest. We trade a few extra files for a sharp boundary between "what we want to happen" and "how we make it happen."

The asymmetry to be aware of: the CRD Go types (`infra/api/v1alpha1`) are *not* pure domain. They are `metav1`-tagged structs because kubebuilder needs them to generate the CRD YAML. The reconciler imports them as the data shape it operates on. If we ever wanted a fully k8s-free domain, we would mirror them into `domain/` — for now, the spec types live in `infra` and that's fine.

### Empty scaffolds

These directories exist (with `.gitkeep`) for upcoming phases but are **not yet implemented**:

| Path                          | Reserved for                                                |
|-------------------------------|-------------------------------------------------------------|
| `controller/webhook/`         | Admission webhook validating `KapeHandler` / `KapeTool`.    |
| `domain/handler/`             | Pure-domain handler types if/when extracted from `infra/api`.|
| `domain/schema/`              | Pure-domain schema types.                                   |
| `domain/tool/`                | Pure-domain tool types.                                     |
| `infra/metrics/`              | Operator-specific Prometheus metrics.                       |
| `infra/probe/`                | Liveness/readiness probe builders for handler pods.         |
| `infra/qdrant/`               | Future: Qdrant collection/user management (currently Qdrant StatefulSet provisioning lives in `infra/k8s/statefulset.go`).|

If you contribute one of these, place it in the directory above and read the *Extending the operator* section for the wiring pattern.

---

## Reconciliation flow

The operator runs three reconcilers:

- **`HandlerReconciler`** (`controller/reconcile/handler.go`) — 12-step KapeHandler reconcile: validates dependencies, renders settings.toml, ensures Deployment, ensures KEDA ScaledObject, syncs labels, updates status.
- **`ToolReconciler`** (`controller/reconcile/tool.go`) — KapeTool reconcile: dispatches on `spec.type` (`memory` → Qdrant StatefulSet; `mcp` → health probe; `event-publish` → type validation). Sets `Ready` status.
- **`SchemaReconciler`** (`controller/reconcile/schema.go`) — KapeSchema reconcile: validates JSON schema, manages a deletion-protection finalizer (`kape.io/schema-protection`), computes `schemaHash`, sets `Ready` status.

Below is one full pass of the `HandlerReconciler`:

```
                        ┌────────────────────────────────────────┐
       watch event ───▶ │ controller-runtime Manager             │
       (KapeHandler,    │  └─ KapeHandlerReconciler.Reconcile()   │  controller/handler.go:30
        owned Dep/CM/SA,│       └─ inner.Reconcile(ctx, key)      │  controller/reconcile/handler.go:67
        KapeTool watch, └────────────────────────────────────────┘
        KapeSchema watch)
                                      │
                                      ▼
                          Step 1: HandlerRepository.Get(ctx, key)
                                      │
                                      ▼
                          Step 2: validateDependencies()     ◀── fetch KapeSchema + KapeTools,
                                      │                          gate on Ready=True
                                      ▼
                          Step 3: validate scaling config
                          (scaleToZero + minReplicas≥1 → terminal error)
                                      │
                                      ▼
                          Step 4: computeRolloutHash(handler, schema, tools)
                          ◀── sha256 over handler.Spec + schema.Spec + each tool.Spec
                                      │
                                      ▼
                          Step 5: KapeConfigLoader.Load + TOMLRenderer.Render
                                + ConfigMapPort.Ensure
                                      │
                                      ▼
                          Step 6: ServiceAccountPort.Ensure  (idempotent)
                                      │
                                      ▼
                          Step 7: DeploymentPort.Ensure      (rolloutHash annotation + sidecar injection)
                                      │
                                      ▼
                          Step 8: ScaledObjectPort.Ensure    ◀── KEDA ScaledObject (NATS JetStream trigger)
                          (delete + recreate if trigger.type changed)
                                      │
                                      ▼
                          Step 9: HandlerRepository.SyncLabels
                          ◀── labels: kape.io/schema-ref, kape.io/tool-ref-*
                                      │
                                      ▼
                          Step 10: HandlerRepository.Get (refresh after label patch)
                                      │
                                      ▼
                          Step 11: DeploymentPort.GetStatus → buildHandlerConditions(...)
                                      │
                                      ▼
                          Step 12: HandlerRepository.UpdateStatus
                          ◀── status sub-resource, RetryOnConflict
                                      │
                                      ▼
                          return Result{RequeueAfter: 60s}, nil
```

A few subtleties worth pointing out:

- **Owner references** are set by `setOwnerRef` in `infra/k8s/configmap.go:81` and reused for the SA and Deployment. That is what makes child deletion automatic when the `KapeHandler` is deleted — *no finalizer is needed for handler cleanup*. Similarly, `setToolOwnerRef` (`infra/k8s/statefulset.go:159`) garbage-collects the Qdrant StatefulSet and Service when a `KapeTool` is deleted.
- **The rollout hash** (`controller/reconcile/handler.go:245`) covers the full handler spec *plus* the resolved KapeSchema and KapeTool specs. Changing any of the three triggers a pod restart without manual action.
- **Dependency gating** (Step 2) means a `KapeHandler` stays `DependenciesReady=False` until all referenced `KapeTool` and `KapeSchema` resources exist and have `Ready=True`. The handler requeues every 30 s while waiting.
- **Cross-resource watches** (`controller/watches.go`) re-enqueue referencing `KapeHandler` objects when a `KapeTool` or `KapeSchema` changes. This is how a schema update propagates to all handlers that reference it.
- **Status updates** use `RetryOnConflict` (`infra/k8s/handler_repo.go:41`) and re-fetch the latest object inside the retry loop. This is the safe pattern for sub-resource updates and avoids stale-version conflict errors.
- **`SyncLabels` patches the spec object** so cross-resource watches can use label selectors. Failure here is non-fatal — status still updates.
- **A fresh cluster works without `kape-config`** — `KapeConfigLoader.Load` returns defaults on `NotFound` (`infra/k8s/kapeconfig.go:32`).
- **`KapeSchema` uses a finalizer** (`kape.io/schema-protection`) that blocks deletion while any `KapeHandler` still references the schema. The finalizer is removed once no handlers reference it.

---

## Key types and interfaces

### CRDs (data)

- `KapeHandler` / `KapeHandlerSpec` / `KapeHandlerStatus` — `operator/infra/api/v1alpha1/kapehandler_types.go:150` (`KapeHandlerSpec` at :150, `KapeHandler` at :222)
- `KapeTool` / `KapeToolSpec` — `operator/infra/api/v1alpha1/kapetool_types.go:100` (`KapeToolSpec` at :100, `KapeTool` at :143)
- `KapeSchema` / `KapeSchemaSpec` — `operator/infra/api/v1alpha1/kapeschema_types.go:30` (`KapeSchemaSpec` at :30, `KapeSchema` at :58)
- `zz_generated.deepcopy.go` — generated by `controller-gen`; do not edit by hand.

### Ports (interfaces the reconcilers depend on)

Defined across `operator/infra/ports/`:

| Interface             | File              | Purpose                                                         |
|-----------------------|-------------------|-----------------------------------------------------------------|
| `HandlerRepository`   | `ports/handler.go`| Get / UpdateStatus / SyncLabels for `KapeHandler`.              |
| `ConfigMapPort`       | `ports/handler.go`| Ensure the `settings.toml` ConfigMap.                           |
| `ServiceAccountPort`  | `ports/handler.go`| Ensure the per-handler ServiceAccount (idempotent create).      |
| `DeploymentPort`      | `ports/handler.go`| Ensure the handler Deployment + read its status.                |
| `KapeConfigLoader`    | `ports/handler.go`| Load cluster-wide defaults from the `kape-config` ConfigMap.    |
| `TOMLRenderer`        | `ports/handler.go`| Render `settings.toml` from a handler spec + cluster config.    |
| `SchemaRepository`    | `ports/schema.go` | Get / UpdateStatus / AddFinalizer / RemoveFinalizer / ListHandlersBySchemaRef. |
| `ToolRepository`      | `ports/tool.go`   | Get / UpdateStatus / ListHandlersByToolRef for `KapeTool`.      |
| `StatefulSetPort`     | `ports/tool.go`   | EnsureQdrant (StatefulSet + headless Service) + GetQdrantReadyReplicas. |
| `ScaledObjectPort`    | `ports/tool.go`   | Ensure / GetConsumerName / Delete for KEDA ScaledObjects.        |

### Adapters (concrete implementations)

| Adapter                  | File                                       | Implements             |
|--------------------------|--------------------------------------------|------------------------|
| `HandlerRepository`      | `operator/infra/k8s/handler_repo.go:17`    | `ports.HandlerRepository` |
| `ConfigMapAdapter`       | `operator/infra/k8s/configmap.go:17`       | `ports.ConfigMapPort`  |
| `ServiceAccountAdapter`  | `operator/infra/k8s/serviceaccount.go:17`  | `ports.ServiceAccountPort` |
| `DeploymentAdapter`      | `operator/infra/k8s/deployment.go:20`      | `ports.DeploymentPort` |
| `KapeConfigLoader`       | `operator/infra/k8s/kapeconfig.go:17`      | `ports.KapeConfigLoader`|
| `Renderer`               | `operator/infra/toml/renderer.go:17`       | `ports.TOMLRenderer`   |
| `SchemaRepository`       | `operator/infra/k8s/schema_repo.go`        | `ports.SchemaRepository` |
| `ToolRepository`         | `operator/infra/k8s/tool_repo.go`          | `ports.ToolRepository` |
| `StatefulSetAdapter`     | `operator/infra/k8s/statefulset.go:20`     | `ports.StatefulSetPort` |
| `ScaledObjectAdapter`    | `operator/infra/k8s/scaledobject.go:26`    | `ports.ScaledObjectPort` |

### Domain values

- `KapeConfig` — `operator/domain/config/config.go:8` — cluster-wide defaults (image refs, NATS endpoint, etc.) with `WithDefaults()` and convenience `*ImageRef()` accessors.

---

## How to run and develop

### Prerequisites

- Go 1.25 (matches `operator/go.mod:3`)
- A Kubernetes cluster you can `kubectl` into. `kind` or `minikube` is fine for dev.
- The kape CRDs installed (`make generate` writes them to `crds/`; apply with `kubectl apply -f crds/`).

### Build

The Go workspace at `go.work` includes `./operator`, `./adapters`, `./task-service`. From the repo root:

```bash
make build           # builds all modules
go build ./operator/cmd/...   # operator only
```

### Run locally

The operator uses `peterbourgon/ff` for flag/env/YAML config. Flags (defaults shown):

```
--metrics-bind-address       :8080
--health-probe-bind-address  :8081
--leader-elect               true
--max-concurrent-reconciles  3
--kape-config-namespace      kape-system
--kape-config-name           kape-config
--config <path>              optional YAML config file
```

Any flag can be set via env with the `KAPE_OPERATOR_` prefix (e.g. `KAPE_OPERATOR_LEADER_ELECT=false`).

```bash
# point at the cluster in your kubeconfig
go run ./operator/cmd --leader-elect=false --metrics-bind-address=:0
```

### Test

```bash
make test                     # all modules
go test ./operator/...        # operator only
```

The hexagonal split means most reconciler tests should use hand-written fakes for the `ports.*` interfaces — no envtest required. Adapter tests against a real apiserver belong in `infra/k8s/` and should use envtest (not yet wired up; see *Empty scaffolds*).

### Container image

Built from the repo root (the Dockerfile copies `go.work` so it can build the workspace):

```bash
podman build -t kape-operator:dev -f operator/Dockerfile .
```

Note: the project's `Makefile` still uses `docker build`. Use `podman` directly per project convention.

### CRD generation

After editing `infra/api/v1alpha1/*_types.go`, regenerate manifests + deepcopy:

```bash
make generate
```

This runs `controller-gen` against `./operator/infra/...` and writes CRDs to `./crds`.

---

## Extending the operator

Three common changes and where they go.

### Add a new field to `KapeHandlerSpec`

1. Edit `operator/infra/api/v1alpha1/kapehandler_types.go` — add the field with the right kubebuilder validation tags (`+kubebuilder:default=...`, `+kubebuilder:validation:...`).
2. Run `make generate` to refresh `zz_generated.deepcopy.go` and the CRD YAML in `crds/`.
3. Decide what consumes the field:
   - If it changes the rendered `settings.toml`: edit `operator/infra/toml/renderer.go` and add the field to the relevant TOML struct.
   - If it changes the Deployment shape: edit `buildDeployment` in `operator/infra/k8s/deployment.go:71`.
   - If it changes status: extend `buildHandlerConditions` in `operator/controller/reconcile/handler.go:264`.
4. The rollout hash in `computeRolloutHash` already covers any new spec field (it hashes the whole `KapeHandlerSpec`), so pods will roll automatically on change. No action needed unless you intentionally want a field excluded from rollout — in which case factor it out of the hash explicitly.

### Add a new owned resource (e.g. a Service)

1. Add a new port to `operator/infra/ports/handler.go` (e.g. `ServicePort`).
2. Implement an adapter under `operator/infra/k8s/` that satisfies it. Use `setOwnerRef` (from `configmap.go:81`) so the resource is GC'd with its `KapeHandler`.
3. Inject the new port into `HandlerReconciler` — extend the constructor in `controller/reconcile/handler.go:42` and add a step to `Reconcile`.
4. Add the type to the controller's watch list — `Owns(&corev1.Service{})` in `SetupHandlerReconciler` (`controller/handler.go:38`) so changes to the Service re-enqueue the parent.
5. Wire the adapter in `cmd/main.go` and pass it to `reconcilehandler.New(...)`.

### Add a new controller (e.g. for a `KapeSkill` CRD)

This is the same pattern used to ship `KapeToolReconciler` and `KapeSchemaReconciler`. Both are live examples you can follow:

1. Create the reconcile algorithm in a new file, e.g. `operator/controller/reconcile/skill.go`. Depend only on ports.
2. Define the ports it needs in a new file under `operator/infra/ports/`.
3. Implement adapters under `operator/infra/k8s/` (and elsewhere as needed).
4. Add a thin `controller-runtime` adapter alongside `controller/handler.go` — a struct with `Reconcile(ctx, req)` that delegates, plus a `SetupSkillReconciler(mgr, ...)` function that calls `ctrl.NewControllerManagedBy(mgr).For(...).Owns(...).Complete(r)`.
5. In `cmd/main.go`, build the dependencies and call `SetupSkillReconciler(mgr, ...)` before `mgr.Start`.

The pattern is: **algorithm → ports → adapters → controller adapter → wiring in main**. Keep each layer small and dependency-free in the upward direction.

---

## File map (cheat sheet)

| File | Lines (approx) | What's in it |
|---|---|---|
| `cmd/main.go` | 144 | Flag parsing, manager construction, dependency wiring, all three `Setup*` calls. |
| `controller/handler.go` | 47 | `KapeHandlerReconciler` (thin) + `SetupHandlerReconciler` (watches + cross-watches). |
| `controller/schema.go` | 36 | `KapeSchemaReconciler` (thin) + `SetupSchemaReconciler`. |
| `controller/tool.go` | 40 | `KapeToolReconciler` (thin) + `SetupToolReconciler` (owns StatefulSet + Service). |
| `controller/watches.go` | 60 | `MapToolToHandlers` / `MapSchemaToHandlers` (cross-resource watch mappers). |
| `controller/reconcile/handler.go` | 303 | 12-step reconcile algorithm + dependency gate + `computeRolloutHash` + `buildHandlerConditions`. |
| `controller/reconcile/schema.go` | 126 | KapeSchema reconcile: JSON validation + finalizer management + `schemaHash`. |
| `controller/reconcile/tool.go` | 196 | KapeTool reconcile: memory/mcp/event-publish dispatch + `probeMCPEndpoint`. |
| `domain/config/config.go` | 92 | `KapeConfig` value type + defaults + image-ref helpers. |
| `infra/api/v1alpha1/kapehandler_types.go` | 242 | CRD types for `KapeHandler`. |
| `infra/api/v1alpha1/kapetool_types.go` | 163 | CRD types for `KapeTool`. |
| `infra/api/v1alpha1/kapeschema_types.go` | 77 | CRD types for `KapeSchema`. |
| `infra/api/v1alpha1/zz_generated.deepcopy.go` | 649 | Generated; do not edit. |
| `infra/ports/handler.go` | 52 | Port interfaces for handler reconciler. |
| `infra/ports/schema.go` | 27 | `SchemaRepository` interface. |
| `infra/ports/tool.go` | 44 | `ToolRepository`, `StatefulSetPort`, `ScaledObjectPort` interfaces. |
| `infra/k8s/handler_repo.go` | 75 | `HandlerRepository` adapter (Get / UpdateStatus / SyncLabels). |
| `infra/k8s/schema_repo.go` | 88 | `SchemaRepository` adapter (Get / UpdateStatus / finalizer / ListHandlersBySchemaRef). |
| `infra/k8s/tool_repo.go` | 61 | `ToolRepository` adapter (Get / UpdateStatus / ListHandlersByToolRef). |
| `infra/k8s/configmap.go` | 94 | `ConfigMapAdapter` + shared `setOwnerRef` helper. |
| `infra/k8s/serviceaccount.go` | 66 | `ServiceAccountAdapter`. |
| `infra/k8s/deployment.go` | 224 | `DeploymentAdapter` + `buildDeployment` (with MCP sidecar injection). |
| `infra/k8s/statefulset.go` | 170 | `StatefulSetAdapter` — Qdrant StatefulSet + headless Service for memory tools. |
| `infra/k8s/scaledobject.go` | 159 | `ScaledObjectAdapter` — KEDA ScaledObject via unstructured client. |
| `infra/k8s/kapeconfig.go` | 58 | `KapeConfigLoader` (reads `kape-system/kape-config`). |
| `infra/toml/renderer.go` | 221 | `Renderer` + private TOML struct tree. |

---

## Related docs

- Repo root [README](../README.md) — what KAPE is and where this fits.
- [Phase 02 — Minimal Operator](../docs/roadmap/phases/02-minimal-operator/README.md) — what was delivered first.
- [Phase 06 — Full Operator](../docs/roadmap/phases/06-full-operator/README.md) — current phase work.
- [Spec 0005 — Kape Operator](../docs/specs/0005-kape-operator/README.md) — design rationale.
- [Spec 0002 — CRDs Design](../docs/specs/0002-crds-design/README.md) — CRD field reference.
