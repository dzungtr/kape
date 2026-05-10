# HandlerReconciler — domain decomposition (functional-core / imperative-shell)

**Status:** draft spec — not yet linked from any phase plan
**Author:** designer (kape-io-phase6)
**Date:** 2026-05-10

## Background

`operator/controller/reconcile/handler.go` has grown to 519 lines / 14 functions
through Phase 6 slices 3–5. `Reconcile` itself spans ~160 lines and mixes
dependency resolution, hash computation, settings.toml rendering, lazy-skill
ConfigMap lifecycle, deployment + scaledobject + serviceaccount + label
syncing, and condition rollup — all in a single function body.

A purely-mechanical file split (issue #51 as written) would address line count
without addressing complexity. The complexity comes from intertwining **what
the handler should be** (a derivation from spec + cluster state) with **how
that gets persisted** (port calls, requeue logic, status patching). Those two
concerns belong on opposite sides of a clean boundary.

`operator/domain/` already exists with a populated `config/` package and empty
`handler/`, `tool/`, `schema/` subdirectories — the architectural intent is
already declared but unrealised.

This spec proposes the realisation: a flat `operator/domain` package holding
behaviour-rich wrappers around the v1alpha1 CRD types, and a thin reconciler
shell that does I/O only.

## Goals

1. **Move every pure decision out of the reconciler.** Validation, hashing,
   prompt assembly, label-set construction, condition rollup, and the gating
   policy that turns "fetched dependency" into "ready/not-ready with reason"
   all become methods on domain types or pure package functions in `domain`.
2. **Keep the reconciler shell flat and explicit.** No umbrella helpers like
   `applyClusterState`. Every k8s-port call appears in `Reconcile` by name, in
   the order the spec requires, exactly once.
3. **Establish the domain layout for future reconcilers.** SchemaReconciler,
   SkillReconciler, ToolReconciler will use the same `domain.NewSchema` /
   `NewSkill` / `NewTool` wrappers and share the same `Condition` /
   `ConditionSet` plumbing.
4. **No behaviour change.** Every existing test passes unmodified. No new
   labels, no new conditions, no requeue-timing changes, no status field
   shape changes.

## Non-goals

- No state machine on `.status.phase`.
- No `Step`/`Strategy` interface for reconcile steps.
- No new CRDs, no new ports, no new k8s adapters.
- No introduction of `KapeProxyReconciler` (slice 6 territory).
- No removal of the legacy `kapetool-{name}` per-tool sidecars (slice 5
  finished the config-rendering side; the `buildSidecars` swap is unrelated
  to this refactor).
- No `Planner`, no `Resolver` service types — domain logic is methods on
  CRD wrappers + a small number of named pure functions, nothing more.

## Architecture

### Layers

```
operator/
  domain/                       k8s-free; depends only on v1alpha1 + stdlib
    config.go                   (already there)
    handler.go                  Handler wrapper + behaviour
    schema.go                   Schema wrapper + behaviour
    skill.go                    Skill wrapper + behaviour
    tool.go                     Tool wrapper + ToolSet + behaviour
    conditions.go               Condition + ConditionSet (package-shared)
    doc.go                      package doc + invariants

  controller/reconcile/         imperative shell; does I/O via ports
    handler.go                  HandlerReconciler.Reconcile (~50 lines, flat)
    handler_fetch.go            small named fetcher helpers (skills, tools)
    handler_status.go           small named status helpers (gate persistence)
    schema.go                   SchemaReconciler   (uses domain.NewSchema)
    skill.go                    SkillReconciler    (uses domain.NewSkill)
    tool.go                     ToolReconciler     (uses domain.NewTool)
    system_prompt.go            DELETED — moved to domain/skill or handler
    testhelpers_test.go         shared findCondition (fixes DuplicateDecl)
```

### Layout principle

`domain/` is a **single Go package** named `domain`. No subpackages. The four
CRD wrappers reference each other (`Handler.SystemPrompt` takes `[]*Skill`,
`Tool.Name` is referenced from `Handler.DesiredLabels`); subpackages would
force either circular imports or upward-extraction into a parent. Files inside
`domain/` are organised by topic for navigability only — they are not
architectural boundaries.

`domain/` may import `k8s.io/apimachinery/pkg/api/meta` (for
`meta.IsStatusConditionTrue` / `meta.FindStatusCondition`) and the operator's
`v1alpha1` CRD types. It must **not** import `controller-runtime`,
`client.Client`, the operator's `infra/ports`, or any infra adapter.

## Domain types

All wrappers follow the same shape:

```go
type Handler struct{ inner *v1alpha1.KapeHandler }
func NewHandler(raw *v1alpha1.KapeHandler) *Handler { return &Handler{inner: raw} }
func (h *Handler) Raw() *v1alpha1.KapeHandler { return h.inner }
```

`Raw()` is the only escape hatch — the reconciler uses it to hand the
underlying object to `repos.Handlers.UpdateStatus` or `Deployments.Ensure`,
both of which want `*v1alpha1.KapeHandler`. Nothing else dereferences it.

### `domain.Handler` — methods

| Group | Method | Purpose |
|---|---|---|
| Identity | `Name() string` / `Namespace() string` | trivial getters |
| References | `SchemaRef() string` / `SchemaKey() types.NamespacedName` | spec.schemaRef + namespaced key |
| References | `ToolRefs() []string` | names from `spec.tools[].ref`, declaration order |
| References | `SkillRefs() []string` | names from `spec.skills[].ref`, declaration order |
| Validation | `ValidateScaling() error` | replaces inline `scaleToZero ∧ minReplicas≥1` check |
| Derivation | `HasLazySkills() bool` | derived from skills (passed in or precomputed) |
| Derivation | `ConsumerName() string` | `strings.ReplaceAll(spec.trigger.type, ".", "-")` |
| Composition | `SystemPrompt(eager, lazy []*Skill) string` | replaces `AssembleSystemPrompt` |
| Composition | `RolloutHash(*Schema, ToolSet, []*Skill) (string, error)` | replaces `computeRolloutHash` |
| Composition | `DesiredLabels(ToolSet) map[string]string` | replaces inline label-build in step 10 |
| Status | `EvaluateDeploymentAvailable(*appsv1.DeploymentStatus, found bool) Condition` | replaces `buildHandlerConditions` body |
| Status | `SetCondition(Condition)` | upsert into `inner.Status.Conditions` |
| Status | `RecomputeReady()` | runs the negative-form rollup; updates Ready in place |
| Status | `Conditions() ConditionSet` | view-only |

`HasLazySkills` takes no argument because it's typically called *after*
skills have been resolved — the wrapper holds them on a side-field after
`Handler.AdoptResolvedSkills(skills)` (set by the reconciler once
`ResolveSkills` returns ready). If we'd rather keep `Handler` immutable, the
method takes `skills []*Skill` directly — choice deferred to implementation;
either works.

### `domain.Schema` / `domain.Tool` / `domain.Skill` — methods

| Type | Method | Purpose |
|---|---|---|
| `Schema` | `Name()`, `IsReady()`, `ReadyMessage()` | identity + Ready predicate |
| `Schema` | `Validate() error` | replaces `validateJSONSchema` |
| `Schema` | `Hash() (string, error)` | replaces `computeSchemaHash` |
| `Tool` | `Name()`, `Type()`, `IsReady()`, `ReadyMessage()` | identity + Ready predicate |
| `Tool` | `ValidateEventPublish() error` | replaces inline `kape.events.` prefix check |
| `Skill` | `Name()`, `Description()`, `Instruction()`, `ToolRefs()` | identity + access |
| `Skill` | `IsLazy()` / `IsEager()` / `IsReady()` / `ReadyMessage()` | predicates |
| `Skill` | `Validate() error` | replaces `validateSkillSpec` |

`MCP probe` (HTTP GET against the upstream URL) stays in
`reconcile/tool.go` — it's I/O and can't be pure. The `tool.go` reconciler
gets its predicate from `Tool.IsReady` after the probe runs.

### `domain.ToolSet`

```go
type ToolSet struct{ byName map[string]*Tool }

func NewToolSet() *ToolSet
func (s *ToolSet) Add(t *Tool)        // no-op if Name() already present (D13)
func (s *ToolSet) Has(name string) bool
func (s *ToolSet) Sorted() []*Tool    // sorted by Tool.Name() — hash stability
func (s *ToolSet) Names() []string    // sorted names
func (s *ToolSet) Len() int
```

Replaces the `map[string]v1alpha1.KapeTool` + `unionToolMap` + `sortedToolsByName`
trio in `handler.go:301-320`.

### `domain.Condition` / `domain.ConditionSet`

```go
type Condition = metav1.Condition  // alias; metav1.Condition is industry-standard

type ConditionSet struct{ conds []Condition }

func (s *ConditionSet) Set(c Condition)             // upsert by Type, preserves LastTransitionTime when status unchanged
func (s *ConditionSet) Find(typ string) (Condition, bool)
func (s *ConditionSet) IsTrue(typ string) bool      // via meta.IsStatusConditionTrue under the hood
func (s *ConditionSet) ReadyRollup() Condition       // negative-form rollup (Ready=True iff no condition is False)
```

`Condition` is a type alias rather than a wrapper struct because
`metav1.Condition` is a stable industry convention used by every Kubernetes
controller; translating to/from a domain-private shape would tax every adapter
without buying isolation that matters.

`Set` replaces the package-shared `setCondition` in `tool.go:184` (which
will be deleted from `tool.go`; the four reconcilers import
`domain.ConditionSet` instead).

### Three named pure functions

These do not belong on any single type — they coordinate across types — so
they live as exported package functions, not methods.

```go
// Step A. Walks h.SkillRefs(), validates that every fetched element is
// non-nil and Ready, returns the assembled list in handler declaration
// order. On any not-ready / missing skill, returns nil + a SkillGate
// describing the first failure.
func ResolveSkills(h *Handler, fetched []*Skill) ([]*Skill, SkillGate)

// Step B. Validates handler-direct tools and skill-pulled tools all Ready,
// then unions them into a ToolSet (handler-direct first, skill-pulled
// after; later inserts of the same Name are no-ops per spec D13).
//   - handlerTools: parallel to h.ToolRefs()
//   - skills:       already-ready skills (output of ResolveSkills)
//   - skillTools:   outer slice parallel to skills; inner slice parallel
//                   to that skill's ToolRefs()
func ResolveTools(
    h *Handler,
    handlerTools []*Tool,
    skills []*Skill,
    skillTools [][]*Tool,
) (*ToolSet, ToolGate)

// Schema readiness check — single fetch, single predicate. Trivial,
// but exposed here so all gating policy lives in one package.
func CheckSchemaReady(s *Schema) SchemaGate
```

Three distinct gate types because they have distinct reasons:

```go
type SkillGate  struct { OK bool; Reason, Message string }   // KapeSkillNotFound | KapeSkillNotReady
type ToolGate   struct { OK bool; Reason, Message string }   // KapeToolNotReady | KapeSkillNotReady (via skill-pulled)
type SchemaGate struct { OK bool; Reason, Message string }   // KapeSchemaInvalid

func (g SkillGate)  AsCondition() Condition
func (g ToolGate)   AsCondition() Condition
func (g SchemaGate) AsCondition() Condition
```

`AsCondition()` builds the `DependenciesReady=False` condition with the
correct `Reason` and `Message`, replacing the four `setCondition(...,
metav1.Condition{Type: "DependenciesReady", Status: False, Reason: ..., ...})`
sites currently scattered through `Reconcile`.

## Reconciler shape

```go
func (r *HandlerReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
    raw, err := r.handlers.Get(ctx, key)
    if err != nil { return ctrl.Result{}, fmt.Errorf("fetching KapeHandler: %w", err) }
    if raw == nil { return ctrl.Result{}, nil }
    h := domain.NewHandler(raw)

    // ── Step A: resolve skills ─────────────────────────────────────────
    fetchedSkills, err := r.fetchSkillsFor(ctx, h)
    if err != nil { return ctrl.Result{}, err }
    skills, skillGate := domain.ResolveSkills(h, fetchedSkills)
    if !skillGate.OK { return r.recordGateAndRequeue(ctx, h, skillGate.AsCondition()) }

    // ── Step B: resolve & union tools ──────────────────────────────────
    handlerTools, err := r.fetchHandlerToolsFor(ctx, h)
    if err != nil { return ctrl.Result{}, err }
    skillTools, err := r.fetchToolsForSkills(ctx, skills)
    if err != nil { return ctrl.Result{}, err }
    tools, toolGate := domain.ResolveTools(h, handlerTools, skills, skillTools)
    if !toolGate.OK { return r.recordGateAndRequeue(ctx, h, toolGate.AsCondition()) }

    // ── Schema check ───────────────────────────────────────────────────
    rawSchema, err := r.schemas.Get(ctx, h.SchemaKey())
    if err != nil { return ctrl.Result{}, fmt.Errorf("fetching KapeSchema: %w", err) }
    schema := domain.NewSchema(rawSchema)
    if g := domain.CheckSchemaReady(schema); !g.OK {
        return r.recordGateAndRequeue(ctx, h, g.AsCondition())
    }

    h.SetCondition(domain.DepsReadyTrue())

    // ── Spec validation ────────────────────────────────────────────────
    if err := h.ValidateScaling(); err != nil {
        h.SetCondition(domain.ScalingInvalid(err))
        h.RecomputeReady()
        return ctrl.Result{}, r.handlers.UpdateStatus(ctx, h.Raw())
    }

    // ── Pure derivations ───────────────────────────────────────────────
    eager, lazy := domain.PartitionSkills(skills)  // small helper, returns (eager, lazy)
    hash, err := h.RolloutHash(schema, tools, skills)
    if err != nil { return ctrl.Result{}, err }
    prompt := h.SystemPrompt(eager, lazy)
    labels := h.DesiredLabels(tools)

    cfg, err := r.kapeConfig.Load(ctx)
    if err != nil { return ctrl.Result{}, err }

    // ── Direct port calls (one per resource, in spec order) ────────────
    toml, err := r.tomlRenderer.Render(h.Raw(), schema.Raw(), tools.Sorted(), eager, lazy, cfg)
    // Note: tomlRenderer.Render currently rebuilds the prompt internally —
    // post-refactor, it should accept the prompt as a parameter so the
    // domain owns it. Tracked as a TOMLRenderer-port shape change below.
    if err != nil { return ctrl.Result{}, err }
    if err := r.configMaps.Ensure(ctx, h.Raw(), toml); err != nil { return ctrl.Result{}, err }

    if h.HasLazySkills(skills) {
        if err := r.skillConfigMaps.Ensure(ctx, h.Raw(), rawSkillsOf(lazy)); err != nil { return ctrl.Result{}, err }
    } else {
        if err := r.skillConfigMaps.Delete(ctx, h.Raw()); err != nil { return ctrl.Result{}, err }
    }

    if err := r.serviceAccounts.Ensure(ctx, h.Raw()); err != nil { return ctrl.Result{}, err }
    if err := r.deployments.Ensure(ctx, h.Raw(), cfg, hash, rawToolsOf(tools.Sorted()), h.HasLazySkills(skills)); err != nil { return ctrl.Result{}, err }
    if err := r.ensureScaledObjectFor(ctx, h, cfg); err != nil { return ctrl.Result{}, err }
    if err := r.handlers.SyncLabels(ctx, h.Raw(), labels); err != nil {
        ctrl.LoggerFrom(ctx).Error(err, "failed to sync labels") // non-fatal, current behaviour
    }

    // ── Re-fetch + status from cluster reality ─────────────────────────
    raw, err = r.handlers.Get(ctx, key)
    if err != nil || raw == nil { return ctrl.Result{}, err }
    h = domain.NewHandler(raw)
    depStatus, found, err := r.deployments.GetStatus(ctx, types.NamespacedName{Name: handlerDeploymentName(h.Raw()), Namespace: h.Namespace()})
    if err != nil { return ctrl.Result{}, err }
    h.SetCondition(h.EvaluateDeploymentAvailable(depStatus, found))
    if found && depStatus != nil { h.Raw().Status.Replicas = depStatus.ReadyReplicas }
    h.RecomputeReady()
    if err := r.handlers.UpdateStatus(ctx, h.Raw()); err != nil { return ctrl.Result{}, err }
    return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}
```

The reconciler is ~55 lines plus three small helpers (`fetchSkillsFor`,
`fetchHandlerToolsFor`, `fetchToolsForSkills`, `recordGateAndRequeue`,
`ensureScaledObjectFor`). The previous 519-line file collapses to roughly
**150 lines of shell + 600 lines of pure domain code**. Total grows because
of the wrapper surface, but every line of growth is testable without envtest.

`ensureScaledObjectFor` is the only k8s-noun helper retained — it wraps the
three-call ScaledObject dance (`GetConsumerName` + conditional `Delete` +
`Ensure`) because that **is** a coherent operation on a single resource and
exists today as a contiguous block in `handler.go:240-253`.

## Required port-shape change

`ports.TOMLRenderer.Render` currently re-derives the system prompt internally
(it calls `AssembleSystemPrompt(handler, eager, lazy)` from
`reconcile/system_prompt.go`). Post-refactor, the prompt is a domain output;
the renderer must accept it as a parameter:

```go
type TOMLRenderer interface {
    Render(
        handler *v1alpha1.KapeHandler,
        schema *v1alpha1.KapeSchema,
        tools []v1alpha1.KapeTool,
        systemPrompt string,        // NEW — replaces internal AssembleSystemPrompt call
        lazySkills []v1alpha1.KapeSkill,  // still needed for [skills.lazy] section
        cfg domainconfig.KapeConfig,
    ) (string, error)
}
```

The infra adapter in `infra/templates/` updates accordingly. This is the only
port-shape change in this refactor.

## Migration plan

The work is mechanically reversible at every step. Order matters.

1. **Create `domain/conditions.go`** — `Condition` alias + `ConditionSet` +
   the negative-form rollup. Tests with table-driven cases.
2. **Create `domain/skill.go` + `domain/tool.go` + `domain/schema.go`** —
   wrappers and predicates. No moves yet; just additions.
3. **Move `validateJSONSchema`, `computeSchemaHash` from
   `reconcile/schema.go`** to `Schema.Validate()` / `Schema.Hash()`. Update
   `SchemaReconciler.Reconcile` to use them. Tests pass.
4. **Move `validateSkillSpec` from `reconcile/skill.go`** to
   `Skill.Validate()`. Update `SkillReconciler`. Tests pass.
5. **Move `setCondition` from `reconcile/tool.go`** into `ConditionSet.Set`.
   Replace all call sites in the four reconcilers. Tests pass.
6. **Replace `findCond` / `isConditionTrue` in
   `reconcile/handler.go` and `isReady` in `reconcile/skill.go`** with
   `meta.FindStatusCondition` / `meta.IsStatusConditionTrue` (used inside
   `domain.ConditionSet`). Delete the private helpers.
7. **Create `domain/handler.go`** — wrapper, identity getters, validation,
   derivation methods. Move `computeRolloutHash`, `computeReadyRollup`,
   `buildHandlerConditions` from `reconcile/handler.go` into Handler methods.
8. **Move `AssembleSystemPrompt` from `reconcile/system_prompt.go`** into
   `Handler.SystemPrompt`. Delete `reconcile/system_prompt.go` (and tests
   move to `domain/handler_test.go`).
9. **Add `ResolveSkills`, `ResolveTools`, `CheckSchemaReady`,
   `PartitionSkills` package functions** to `domain/`. Tests cover every
   gate path.
10. **Rewrite `reconcile/handler.go.Reconcile`** to the shape above. Extract
    `fetchSkillsFor`, `fetchHandlerToolsFor`, `fetchToolsForSkills`,
    `recordGateAndRequeue`, `ensureScaledObjectFor` as small unexported
    methods.
11. **Update `ports.TOMLRenderer.Render`** signature to accept
    `systemPrompt string`. Update infra adapter. Update reconciler call
    site to pass the domain-derived prompt.
12. **Add `testhelpers_test.go`** with shared `findCondition` (or replace
    all test-side calls with `meta.FindStatusCondition` directly — author
    discretion in step 12).
13. **Delete the now-empty `domain/handler/`, `domain/schema/`,
    `domain/tool/` subdirectories** (and their `.gitkeep` files). The flat
    `domain/` package is now the only layout.

After every step: `go test ./operator/...` must pass. No step is allowed to
land with red tests.

## Acceptance criteria

- [ ] `operator/controller/reconcile/handler.go` ≤ 200 lines total
- [ ] `Reconcile` body ≤ 60 lines
- [ ] `operator/domain/` is a single flat Go package; no subpackages remain
- [ ] `operator/domain/` has no imports from `controller-runtime`,
      `client.Client`, `infra/ports`, or `infra/k8s`
- [ ] `findCond`, `isConditionTrue`, `isReady`, `findCondition` (test
      duplicates), `unionToolMap`, `sortedToolsByName`, `setCondition`,
      `computeRolloutHash`, `computeReadyRollup`, `buildHandlerConditions`,
      `validateJSONSchema`, `computeSchemaHash`, `validateSkillSpec`,
      `AssembleSystemPrompt` all deleted from `controller/reconcile/` —
      replaced by domain methods or apimachinery helpers
- [ ] `meta.IsStatusConditionTrue` / `meta.FindStatusCondition` used in
      place of every hand-rolled equivalent
- [ ] `ports.TOMLRenderer.Render` takes `systemPrompt string`; the renderer
      no longer re-derives the prompt
- [ ] All existing tests pass without modification *except* tests for
      moved functions, which move alongside their code
- [ ] `go test ./operator/...` green; `go vet ./operator/...` clean
- [ ] No new linter errors; the existing `DuplicateDecl` for `findCondition`
      is resolved
- [ ] No new ports, no new adapters, no new CRDs, no new conditions, no new
      labels, no requeue-timing changes

## Risks

| Risk | Mitigation |
|---|---|
| Wrapper surface (`NewHandler`/`Raw`) leaks through tests, making them harder to write | Domain tests use `NewHandler(&v1alpha1.KapeHandler{Spec: ...})` directly — same shape as today's tests build the raw object |
| Forgetting to call `RecomputeReady()` after `SetCondition()` causes Ready drift | Add a single `domain/handler_test.go` test that asserts every public Handler method that mutates conditions also calls `RecomputeReady()` internally — or change `SetCondition` to recompute eagerly |
| `Handler.HasLazySkills(skills)` API forces the caller to pass skills every time, awkward in deeply-nested helpers | Either (a) take the param explicitly (current proposal), or (b) add `Handler.AdoptResolvedSkills(skills)` to attach them to the wrapper. Defer choice to implementation; both compile and test cleanly |
| `ResolveTools` signature with `[][]*Tool` is unfamiliar | Document with example in godoc; alternative is a `ResolvedSkill struct{ Skill *Skill; Tools []*Tool }` value type — defer to implementation review |
| `TOMLRenderer.Render` shape change touches infra/templates + tests | One contained change in step 11; renderer tests update once. No external-API surface — `Render` is internal to operator |
| Status-only writebacks miss the gate-failure path's early `_ = r.handlers.UpdateStatus(...)` (current code's "best-effort" pattern) | Preserve exactly via `recordGateAndRequeue` helper — same `_ = ...` ignore semantics, same `RequeueAfter: 30 * time.Second` |

## Open questions deferred to implementation

These do not block spec approval; either choice satisfies the criteria. The
implementing agent picks one and notes the reason in the PR.

1. **Wrap vs. type-define for `Handler` etc.**
   - `type Handler struct{ inner *v1alpha1.KapeHandler }` — explicit, clear
     boundary, requires `.inner` everywhere.
   - `type Handler v1alpha1.KapeHandler` — zero-cost conversion, methods
     attach directly, but reading `(*domain.Handler)(raw)` looks unusual.
   - **Default:** wrap (struct with `inner` + `Raw()`). Switch only if the
     wrapper boilerplate causes friction during step 7.

2. **`Handler.HasLazySkills` — parameter or stateful**
   - `func (h *Handler) HasLazySkills(skills []*Skill) bool` (functional)
   - `func (h *Handler) AdoptResolvedSkills(skills []*Skill); HasLazySkills() bool` (stateful)
   - **Default:** functional. Switch only if call sites pile up.

3. **`ResolveTools` parallel-slice signature**
   - Current proposal: `(handlerTools []*Tool, skills []*Skill, skillTools [][]*Tool)`
   - Alternative: `(handlerTools []*Tool, resolved []ResolvedSkill)` where
     `ResolvedSkill struct{ Skill *Skill; Tools []*Tool }`.
   - **Default:** parallel slices. Switch if godoc examples become hard
     to follow.

4. **Test-side `findCondition` removal — shared helper or apimachinery direct**
   - `testhelpers_test.go` with one shared `findCondition`.
   - Replace every test call with `meta.FindStatusCondition` inline.
   - **Default:** apimachinery direct. Removing the helper entirely removes
     the future risk of someone re-adding a duplicate.

## What this spec does not cover

- `KapeProxyReconciler` (slice 6) — uses the same domain layout, but
  designing it is out of scope here.
- The legacy `kapetool-{name}` per-tool sidecar swap (slice 5's design
  intent that didn't land) — orthogonal to this refactor.
- Any change to CRD types in `infra/api/v1alpha1/` — domain wraps existing
  shapes, doesn't redesign them.
- Any introduction of webhooks (`controller/webhook/` is empty today).
