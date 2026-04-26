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

What it currently does **not** do (scaffolded but not implemented — see *Empty scaffolds* below): no `KapeTool` reconciler, no `KapeSchema` reconciler, no admission webhook, no liveness/readiness probe builders for the handler pod, no Qdrant provisioning, no operator-side metrics beyond what controller-runtime publishes by default.

The CRDs the operator owns are defined in `operator/infra/api/v1alpha1/`:

- `KapeHandler` — `operator/infra/api/v1alpha1/kapehandler_types.go:217` — primary CRD; one handler = one agent pipeline.
- `KapeTool` — `operator/infra/api/v1alpha1/kapetool_types.go:131` — declares an MCP, memory, or event-publish tool capability.
- `KapeSchema` — `operator/infra/api/v1alpha1/kapeschema_types.go:53` — the JSON Schema contract for the LLM's structured output.

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
│   └── reconcile/
│       └── handler.go      #   the actual reconcile algorithm (depends on ports)
└── infra/                  # everything that touches Kubernetes or external systems
    ├── api/v1alpha1/       #   CRD Go types + zz_generated.deepcopy.go
    ├── ports/              #   outbound interfaces the reconciler depends on
    ├── k8s/                #   adapters that satisfy ports using sigs.k8s.io/client
    └── toml/               #   adapter that renders settings.toml
```

| Directory                | Role                                   | Depends on                              |
|--------------------------|----------------------------------------|-----------------------------------------|
| `cmd/`                   | Parse flags, build manager, wire deps. | Everything below.                       |
| `domain/`                | Plain values and rules. No k8s.        | Nothing in this module.                 |
| `controller/`            | controller-runtime glue.               | `controller/reconcile`, `infra/api`.    |
| `controller/reconcile/`  | The reconcile algorithm.               | `infra/ports`, `infra/api`, `domain`.   |
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
| `infra/qdrant/`               | Qdrant collection provisioning for `KapeTool` of type memory.|

If you contribute one of these, place it in the directory above and read the *Extending the operator* section for the wiring pattern.

---

## Reconciliation flow

There is currently one reconciler: `HandlerReconciler` for `KapeHandler`. Here is one full pass.

```
                        ┌────────────────────────────────────────┐
       watch event ───▶ │ controller-runtime Manager             │
       (KapeHandler,    │  └─ KapeHandlerReconciler.Reconcile()   │  controller/handler.go:29
        owned Dep,      │       └─ inner.Reconcile(ctx, key)      │  controller/reconcile/handler.go:51
        owned CM, SA)   └────────────────────────────────────────┘
                                      │
                                      ▼
                          ┌──────────────────────┐
                          │ HandlerRepository    │  Get(ctx, key)
                          └──────────────────────┘
                                      │
                                      ▼
                          ┌──────────────────────┐
                          │ KapeConfigLoader     │  Load(ctx) → kape-config defaults
                          └──────────────────────┘
                                      │
                                      ▼
                          computeRolloutHash(spec)        ◀── sha256 of marshalled spec
                                      │
                                      ▼
                          ┌──────────────────────┐
                          │ TOMLRenderer         │  Render(handler, cfg) → settings.toml
                          └──────────────────────┘
                                      │
                                      ▼
                          ┌──────────────────────┐
                          │ ConfigMapPort.Ensure │  create or patch settings.toml CM
                          └──────────────────────┘
                                      │
                                      ▼
                          ┌──────────────────────┐
                          │ ServiceAccountPort   │  create-if-absent (idempotent)
                          │      .Ensure         │
                          └──────────────────────┘
                                      │
                                      ▼
                          ┌──────────────────────┐
                          │ DeploymentPort.Ensure│  create or patch with rollout-hash annotation
                          └──────────────────────┘
                                      │
                                      ▼
                          HandlerRepository.SyncLabels      ◀── label kape.io/schema-ref, tool-ref-*
                                      │
                                      ▼
                          DeploymentPort.GetStatus
                                      │
                                      ▼
                          buildConditions(...) → handler.Status.Conditions
                                      │
                                      ▼
                          HandlerRepository.UpdateStatus    ◀── status sub-resource, RetryOnConflict
                                      │
                                      ▼
                          return Result{RequeueAfter: 60s}, nil
```

A few subtleties worth pointing out:

- **Owner references** are set by `setOwnerRef` in `infra/k8s/configmap.go:81` and reused for the SA and Deployment. That is what makes child deletion automatic when the `KapeHandler` is deleted — *no finalizer is needed for cleanup*.
- **The rollout hash** (`controller/reconcile/handler.go:139`) is a sha256 of the marshalled spec, written as a pod-template annotation. Changing it forces a rolling pod restart; identical specs do nothing. This is the standard "stable hash → annotation" trick to bind config changes to pod rollouts.
- **Status updates** use `RetryOnConflict` (`infra/k8s/handler_repo.go:41`) and re-fetch the latest object inside the retry loop. This is the safe pattern for sub-resource updates and avoids stale-version conflict errors.
- **`SyncLabels` patches the spec object** so cross-resource watches can use label selectors. Failure here is non-fatal — status still updates.
- **A fresh cluster works without `kape-config`** — `KapeConfigLoader.Load` returns defaults on `NotFound` (`infra/k8s/kapeconfig.go:32`).

---

## Key types and interfaces

### CRDs (data)

- `KapeHandler` / `KapeHandlerSpec` / `KapeHandlerStatus` — `operator/infra/api/v1alpha1/kapehandler_types.go:150`
- `KapeTool` / `KapeToolSpec` — `operator/infra/api/v1alpha1/kapetool_types.go:93` (CRD types only; no reconciler yet)
- `KapeSchema` / `KapeSchemaSpec` — `operator/infra/api/v1alpha1/kapeschema_types.go:30` (CRD types only)
- `zz_generated.deepcopy.go` — generated by `controller-gen`; do not edit by hand.

### Ports (interfaces the reconciler depends on)

All defined in `operator/infra/ports/handler.go`:

| Interface             | Purpose                                                         |
|-----------------------|-----------------------------------------------------------------|
| `HandlerRepository`   | Get / UpdateStatus / SyncLabels for `KapeHandler`.              |
| `ConfigMapPort`       | Ensure the `settings.toml` ConfigMap.                           |
| `ServiceAccountPort`  | Ensure the per-handler ServiceAccount (idempotent create).      |
| `DeploymentPort`      | Ensure the handler Deployment + read its status.                |
| `KapeConfigLoader`    | Load cluster-wide defaults from the `kape-config` ConfigMap.    |
| `TOMLRenderer`        | Render `settings.toml` from a handler spec + cluster config.    |

### Adapters (concrete implementations)

| Adapter                  | File                                       | Implements             |
|--------------------------|--------------------------------------------|------------------------|
| `HandlerRepository`      | `operator/infra/k8s/handler_repo.go:17`    | `ports.HandlerRepository` |
| `ConfigMapAdapter`       | `operator/infra/k8s/configmap.go:17`       | `ports.ConfigMapPort`  |
| `ServiceAccountAdapter`  | `operator/infra/k8s/serviceaccount.go:17`  | `ports.ServiceAccountPort` |
| `DeploymentAdapter`      | `operator/infra/k8s/deployment.go:20`      | `ports.DeploymentPort` |
| `KapeConfigLoader`       | `operator/infra/k8s/kapeconfig.go:17`      | `ports.KapeConfigLoader`|
| `Renderer`               | `operator/infra/toml/renderer.go:17`       | `ports.TOMLRenderer`   |

### Domain values

- `KapeConfig` — `operator/domain/config/config.go:8` — cluster-wide defaults (image refs, NATS endpoint, etc.) with `WithDefaults()` and convenience `*ImageRef()` accessors.

---

## How to run and develop

### Prerequisites

- Go 1.24 (matches `operator/go.mod:3`)
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
   - If it changes the Deployment shape: edit `buildDeployment` in `operator/infra/k8s/deployment.go:81`.
   - If it changes status: extend `buildConditions` in `operator/controller/reconcile/handler.go:148`.
4. The rollout hash in `computeRolloutHash` already covers any new spec field (it hashes the whole `KapeHandlerSpec`), so pods will roll automatically on change. No action needed unless you intentionally want a field excluded from rollout — in which case factor it out of the hash explicitly.

### Add a new owned resource (e.g. a Service)

1. Add a new port to `operator/infra/ports/handler.go` (e.g. `ServicePort`).
2. Implement an adapter under `operator/infra/k8s/` that satisfies it. Use `setOwnerRef` (from `configmap.go:81`) so the resource is GC'd with its `KapeHandler`.
3. Inject the new port into `HandlerReconciler` — extend the constructor in `controller/reconcile/handler.go:32` and add a step to `Reconcile`.
4. Add the type to the controller's watch list — `Owns(&corev1.Service{})` in `SetupHandlerReconciler` (`controller/handler.go:36`) so changes to the Service re-enqueue the parent.
5. Wire the adapter in `cmd/main.go` and pass it to `reconcilehandler.New(...)`.

### Add a new controller (e.g. for `KapeTool`)

1. Create the reconcile algorithm in a new package, e.g. `operator/controller/reconcile/tool/handler.go`. Depend only on ports.
2. Define the ports it needs in `operator/infra/ports/`.
3. Implement adapters under `operator/infra/k8s/` (and elsewhere as needed).
4. Add a thin `controller-runtime` adapter alongside `controller/handler.go` — a struct with `Reconcile(ctx, req)` that delegates, plus a `SetupToolReconciler(mgr, ...)` function that calls `ctrl.NewControllerManagedBy(mgr).For(...).Owns(...).Complete(r)`.
5. In `cmd/main.go`, build the dependencies and call `SetupToolReconciler(mgr, ...)` before `mgr.Start`.

The pattern is: **algorithm → ports → adapters → controller adapter → wiring in main**. Keep each layer small and dependency-free in the upward direction.

---

## File map (cheat sheet)

| File | Lines (approx) | What's in it |
|---|---|---|
| `cmd/main.go` | 122 | Flag parsing, manager construction, dependency wiring, `Start`. |
| `controller/handler.go` | 47 | `KapeHandlerReconciler` (thin) + `SetupHandlerReconciler` (watches). |
| `controller/reconcile/handler.go` | 198 | Full reconcile algorithm + `computeRolloutHash` + `buildConditions`. |
| `domain/config/config.go` | 76 | `KapeConfig` value type + defaults + image-ref helpers. |
| `infra/api/v1alpha1/kapehandler_types.go` | 237 | CRD types for `KapeHandler` (the only one with a reconciler today). |
| `infra/api/v1alpha1/kapetool_types.go` | 151 | CRD types for `KapeTool`. |
| `infra/api/v1alpha1/kapeschema_types.go` | 72 | CRD types for `KapeSchema`. |
| `infra/api/v1alpha1/zz_generated.deepcopy.go` | 647 | Generated; do not edit. |
| `infra/ports/handler.go` | 55 | Port interfaces consumed by the reconciler. |
| `infra/k8s/handler_repo.go` | 75 | `HandlerRepository` adapter (Get / UpdateStatus / SyncLabels). |
| `infra/k8s/configmap.go` | 94 | `ConfigMapAdapter` + shared `setOwnerRef` helper. |
| `infra/k8s/serviceaccount.go` | 66 | `ServiceAccountAdapter`. |
| `infra/k8s/deployment.go` | 175 | `DeploymentAdapter` + `buildDeployment`. |
| `infra/k8s/kapeconfig.go` | 55 | `KapeConfigLoader` (reads `kape-system/kape-config`). |
| `infra/toml/renderer.go` | 164 | `Renderer` + private TOML struct tree. |

---

## Related docs

- Repo root [README](../README.md) — what KAPE is and where this fits.
- [Phase 02 — Minimal Operator](../docs/roadmap/phases/02-minimal-operator/README.md) — what was delivered first.
- [Phase 06 — Full Operator](../docs/roadmap/phases/06-full-operator/README.md) — current phase work.
- [Spec 0005 — Kape Operator](../docs/specs/0005-kape-operator/README.md) — design rationale.
- [Spec 0002 — CRDs Design](../docs/specs/0002-crds-design/README.md) — CRD field reference.
