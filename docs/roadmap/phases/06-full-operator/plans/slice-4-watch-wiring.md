# Phase 6 Slice 4 — KapeSkill Cross-Resource Watch Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire a secondary watch so that any change to a `KapeSkill` resource automatically re-enqueues every `KapeHandler` that references it, verified by observing the rollout-hash annotation change on the referencing handler's Deployment within one reconcile cycle.

**Architecture:** Add `MapSkillToHandlers(c client.Client)` to `operator/controller/watches.go`, mirroring the existing `MapToolToHandlers` and `MapSchemaToHandlers` functions. It uses the label `kape.io/skill-ref-{name}=true` written by Slice 3. Register the new mapper in `SetupHandlerReconciler` in `operator/controller/handler.go` alongside the existing KapeTool and KapeSchema watches. No label-writing happens in this slice — Slice 3 owns label sync.

**Tech Stack:** Go 1.25, controller-runtime v0.19, sigs.k8s.io/controller-runtime/pkg/handler, controller-runtime fake client for unit tests, envtest for integration tests, Snyk MCP tools for security scanning.

---

## Prerequisite: Slice 3 contract

Slice 4's watch reads `kape.io/skill-ref-{name}=true` labels written by Slice 3's label-sync step (Step 9 in the handler reconciler). The unit tests in this slice use a fake client and pre-label the handler objects directly. The envtest test pre-labels the handler via a direct `client.Patch` or uses a running reconciler that wrote the label during a prior reconcile. **This slice does not write any labels itself.**

Additionally, Slice 4 assumes `KapeSkill` is registered in the scheme (done by Slice 1). The unit tests must add `KapeSkill` / `KapeSkillList` to the test scheme.

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `operator/controller/watches.go` | **Modify** | Add `MapSkillToHandlers` function |
| `operator/controller/handler.go` | **Modify** | Register `KapeSkill` watch in `SetupHandlerReconciler` |
| `operator/controller/watches_test.go` | **Create** | Unit tests for all three mapper functions |

---

## Task 1: Add `MapSkillToHandlers` to `watches.go`

**Files:**
- Modify: `operator/controller/watches.go`

- [ ] **Step 1: Write the failing test first** (in `watches_test.go` — create this file)

Create `operator/controller/watches_test.go`:

```go
package controller_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kape-io/kape/operator/controller"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

func newWatchScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	return s
}

// handlerWithLabel creates a KapeHandler with the given label key+value already set.
func handlerWithLabel(name, ns, labelKey, labelVal string) *v1alpha1.KapeHandler {
	return &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{labelKey: labelVal},
		},
	}
}

func TestMapSkillToHandlers_EnqueuesLabelledHandlers(t *testing.T) {
	s := newWatchScheme()

	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: "kape-system"},
	}
	// h1 has the skill-ref label; h2 does not.
	h1 := handlerWithLabel("handler-a", "kape-system", "kape.io/skill-ref-analyst", "true")
	h2 := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "handler-b", Namespace: "kape-system"},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(skill, h1, h2).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), skill)

	require.Len(t, requests, 1)
	assert.Equal(t, types.NamespacedName{Name: "handler-a", Namespace: "kape-system"}, requests[0].NamespacedName)
}

func TestMapSkillToHandlers_NoHandlers_ReturnsEmpty(t *testing.T) {
	s := newWatchScheme()
	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: "kape-system"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(skill).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), skill)

	assert.Empty(t, requests)
}

func TestMapSkillToHandlers_WrongObjectType_ReturnsNil(t *testing.T) {
	s := newWatchScheme()
	// Pass a KapeHandler as the triggering object — must return nil, not panic.
	notASkill := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "handler-a", Namespace: "kape-system"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(notASkill).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), notASkill)

	assert.Nil(t, requests)
}

func TestMapSkillToHandlers_MultipleHandlers_EnqueuesAll(t *testing.T) {
	s := newWatchScheme()
	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: "kape-system"},
	}
	h1 := handlerWithLabel("handler-a", "kape-system", "kape.io/skill-ref-analyst", "true")
	h2 := handlerWithLabel("handler-b", "kape-system", "kape.io/skill-ref-analyst", "true")
	h3 := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "handler-c", Namespace: "kape-system"},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(skill, h1, h2, h3).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), skill)

	require.Len(t, requests, 2)
	names := []string{requests[0].NamespacedName.Name, requests[1].NamespacedName.Name}
	assert.Contains(t, names, "handler-a")
	assert.Contains(t, names, "handler-b")
}
```

- [ ] **Step 2: Run the test to confirm it fails to compile** (function doesn't exist yet)

```bash
cd /home/tony/projects/kape-io && go test ./operator/controller/... 2>&1 | head -20
```

Expected: compile error — `controller.MapSkillToHandlers undefined`

- [ ] **Step 3: Add `MapSkillToHandlers` to `watches.go`**

Open `operator/controller/watches.go` and append the new function after `MapSchemaToHandlers`:

```go
// MapSkillToHandlers maps a KapeSkill change to the KapeHandlers that reference it.
// Used as a secondary watch: KapeSkill changes re-enqueue referencing KapeHandlers.
// Depends on Slice 3 having written kape.io/skill-ref-{name}=true labels on KapeHandler resources.
func MapSkillToHandlers(c client.Client) func(ctx context.Context, obj client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		skill, ok := obj.(*v1alpha1.KapeSkill)
		if !ok {
			return nil
		}
		var handlerList v1alpha1.KapeHandlerList
		if err := c.List(ctx, &handlerList, client.MatchingLabels{
			fmt.Sprintf("kape.io/skill-ref-%s", skill.Name): "true",
		}); err != nil {
			return nil
		}
		requests := make([]reconcile.Request, 0, len(handlerList.Items))
		for _, h := range handlerList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: h.Name, Namespace: h.Namespace},
			})
		}
		return requests
	}
}
```

Note: `KapeSkill` and `KapeSkillList` are defined by Slice 1 in `operator/infra/api/v1alpha1/kapeskill_types.go`. The imports (`fmt`, `context`, `k8s.io/apimachinery/pkg/types`, `sigs.k8s.io/controller-runtime/pkg/client`, `sigs.k8s.io/controller-runtime/pkg/reconcile`, `v1alpha1`) are already present in `watches.go` from the existing functions — no new imports needed beyond adding `KapeSkill` to the scheme.

- [ ] **Step 4: Run the unit tests to confirm they pass**

```bash
cd /home/tony/projects/kape-io && go test ./operator/controller/... -run TestMapSkillToHandlers -v 2>&1
```

Expected output:
```
=== RUN   TestMapSkillToHandlers_EnqueuesLabelledHandlers
--- PASS: TestMapSkillToHandlers_EnqueuesLabelledHandlers
=== RUN   TestMapSkillToHandlers_NoHandlers_ReturnsEmpty
--- PASS: TestMapSkillToHandlers_NoHandlers_ReturnsEmpty
=== RUN   TestMapSkillToHandlers_WrongObjectType_ReturnsNil
--- PASS: TestMapSkillToHandlers_WrongObjectType_ReturnsNil
=== RUN   TestMapSkillToHandlers_MultipleHandlers_EnqueuesAll
--- PASS: TestMapSkillToHandlers_MultipleHandlers_EnqueuesAll
PASS
```

- [ ] **Step 5: Run the full operator test suite to confirm no regressions**

```bash
cd /home/tony/projects/kape-io && go test ./operator/... 2>&1
```

Expected: all tests pass with `ok`.

- [ ] **Step 6: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice4-plan add operator/controller/watches.go operator/controller/watches_test.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice4-plan commit -m "feat(operator): add MapSkillToHandlers secondary watch mapper"
```

---

## Task 2: Register the KapeSkill watch in `handler.go`

**Files:**
- Modify: `operator/controller/handler.go`

- [ ] **Step 1: Write the failing test for watch registration**

Add `TestSetupHandlerReconciler_RegistersKapeSkillWatch` to `operator/controller/watches_test.go`. Because `SetupHandlerReconciler` requires a live manager and scheme, this test uses `envtest` and is placed in a separate `_envtest_test.go` suffix file (to keep unit tests runnable without `KUBEBUILDER_ASSETS`).

However, direct watch-registration testing is impractical without a running manager. The canonical approach is: verify the watch fires by running an envtest scenario (Task 3). For this step, we assert the function compiles and the wiring is present by reading the source — covered by Task 3's envtest test.

Skip the unit-level compilation test; proceed to wiring.

- [ ] **Step 2: Add the KapeSkill watch to `SetupHandlerReconciler`**

Open `operator/controller/handler.go`. In `SetupHandlerReconciler`, add the `KapeSkill` watch line after the existing `KapeSchema` watch:

```go
func SetupHandlerReconciler(mgr manager.Manager, inner *reconcilehandler.HandlerReconciler, maxConcurrent int) error {
	r := NewKapeHandlerReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeHandler{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Watches(&v1alpha1.KapeTool{}, handler.EnqueueRequestsFromMapFunc(MapToolToHandlers(mgr.GetClient()))).
		Watches(&v1alpha1.KapeSchema{}, handler.EnqueueRequestsFromMapFunc(MapSchemaToHandlers(mgr.GetClient()))).
		Watches(&v1alpha1.KapeSkill{}, handler.EnqueueRequestsFromMapFunc(MapSkillToHandlers(mgr.GetClient()))).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
```

The only change is the new `.Watches(&v1alpha1.KapeSkill{}, ...)` line. All imports (`v1alpha1`, `handler`) are already present.

- [ ] **Step 3: Verify compilation**

```bash
cd /home/tony/projects/kape-io && go build ./operator/... 2>&1
```

Expected: no output (clean build).

- [ ] **Step 4: Run full test suite again**

```bash
cd /home/tony/projects/kape-io && go test ./operator/... 2>&1
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice4-plan add operator/controller/handler.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice4-plan commit -m "feat(operator): register KapeSkill watch in SetupHandlerReconciler"
```

---

## Task 3: Envtest integration test — KapeSkill spec change triggers handler reconcile

**Files:**
- Create: `operator/controller/watches_envtest_test.go`

This test verifies the Definition of Done: editing a KapeSkill's spec triggers a reconcile of every referencing KapeHandler, observed via the rollout-hash annotation change on the handler's Deployment.

**How the test works:**

1. Start envtest with all CRDs installed.
2. Apply a `KapeSchema` (Ready), a `KapeSkill`, and a `KapeHandler` whose handler has the label `kape.io/skill-ref-<skill-name>=true` pre-applied (simulating what Slice 3 writes).
3. Run the full `HandlerReconciler` via the manager.
4. Record the initial rollout-hash annotation from the handler's Deployment.
5. Patch the `KapeSkill` spec (e.g. change `instruction` field).
6. Wait up to 10 seconds for the Deployment's rollout-hash annotation to change.
7. Assert the annotation has a new non-empty value different from the initial one.

**Important note on Slice 3 dependency:** The watch fires because of the label `kape.io/skill-ref-{name}=true` on the handler. In this slice's envtest test, we pre-apply that label directly to the handler object before starting the manager. The full label-writing logic lives in Slice 3 — this slice only verifies the watch fires when the label is present.

**Note on KapeSkill types:** `KapeSkill` is defined by Slice 1. If Slice 1 is not yet merged, the test uses a placeholder struct that satisfies the `client.Object` interface. When Slice 1 lands, replace the placeholder with `v1alpha1.KapeSkill`. The plan below assumes Slice 1 types are available.

- [ ] **Step 1: Write the envtest test file**

Create `operator/controller/watches_envtest_test.go`:

```go
//go:build envtest

package controller_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	kapecontroller "github.com/kape-io/kape/operator/controller"
	reconcilehandler "github.com/kape-io/kape/operator/controller/reconcile"
	domainconfig "github.com/kape-io/kape/operator/domain/config"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	tomlrenderer "github.com/kape-io/kape/operator/infra/toml"
)

var enqueueScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(appsv1.AddToScheme(s))
	utilruntime.Must(corev1.AddToScheme(s))
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}()

func TestSkillWatch_SpecChange_TriggersHandlerReconcile(t *testing.T) {
	env := &envtest.Environment{
		CRDDirectoryPaths: []string{"../../infra/api/v1alpha1/crds"},
		Scheme:            enqueueScheme,
	}
	restCfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:                 enqueueScheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	require.NoError(t, err)

	k8sClient := mgr.GetClient()
	platformCfg := domainconfig.KapeConfig{
		ClusterName:         "envtest",
		HandlerImage:        "kape-runtime",
		HandlerImageVersion: "dev",
		NatsURL:             "nats://localhost:4222",
	}
	cfgLoader := &staticConfigLoader{cfg: platformCfg}

	innerReconciler := reconcilehandler.NewHandlerReconciler(
		k8sadapters.NewHandlerRepository(k8sClient),
		k8sadapters.NewSchemaRepository(k8sClient),
		k8sadapters.NewToolRepository(k8sClient),
		k8sadapters.NewConfigMapAdapter(k8sClient),
		k8sadapters.NewServiceAccountAdapter(k8sClient),
		k8sadapters.NewDeploymentAdapter(k8sClient),
		k8sadapters.NewScaledObjectAdapter(k8sClient),
		tomlrenderer.NewRenderer(),
		cfgLoader,
	)
	err = kapecontroller.SetupHandlerReconciler(mgr, innerReconciler, 1)
	require.NoError(t, err)

	go func() { _ = mgr.Start(ctx) }()

	ns := "default"

	// 1. Apply a Ready KapeSchema
	schema := &v1alpha1.KapeSchema{
		ObjectMeta: metav1.ObjectMeta{Name: "test-schema", Namespace: ns},
		Spec: v1alpha1.KapeSchemaSpec{
			Version: "v1",
			JSONSchema: v1alpha1.JSONSchemaObject{
				Type:     "object",
				Required: []string{"msg"},
			},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, schema))
	schema.Status.Conditions = []metav1.Condition{{
		Type: "Ready", Status: metav1.ConditionTrue, Reason: "Valid",
	}}
	require.NoError(t, k8sClient.Status().Update(ctx, schema))

	// 2. Apply a KapeSkill.
	// Field names (Description, Instruction) must match v1alpha1.KapeSkillSpec defined by Slice 1.
	// Verify against operator/infra/api/v1alpha1/kapeskill_types.go before running.
	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: ns},
		Spec: v1alpha1.KapeSkillSpec{
			Description: "Analyst skill",
			Instruction: "You are an analyst.",
		},
	}
	require.NoError(t, k8sClient.Create(ctx, skill))

	// 3. Apply a KapeHandler with the skill-ref label pre-set (Slice 3 owns writing this label;
	//    here we simulate it being present to test the watch fires).
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-handler",
			Namespace: ns,
			Labels:    map[string]string{"kape.io/skill-ref-analyst": "true"},
		},
		Spec: v1alpha1.KapeHandlerSpec{
			Trigger:   v1alpha1.TriggerSpec{Source: "alertmanager", Type: "kape.events.test"},
			LLM:       v1alpha1.LLMSpec{Provider: "anthropic", Model: "claude-3", SystemPrompt: "test"},
			SchemaRef: "test-schema",
			Tools:     []v1alpha1.ToolRef{},
			Actions:   []v1alpha1.ActionSpec{},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, handler))

	// 4. Wait for initial reconcile to create the Deployment.
	depKey := types.NamespacedName{Name: "kape-handler-my-handler", Namespace: ns}
	var initialDep appsv1.Deployment
	require.Eventually(t, func() bool {
		err := k8sClient.Get(ctx, depKey, &initialDep)
		return err == nil && initialDep.Annotations["kape.io/rollout-hash"] != ""
	}, 10*time.Second, 200*time.Millisecond, "deployment should be created with rollout-hash annotation")

	initialHash := initialDep.Annotations["kape.io/rollout-hash"]

	// 5. Patch the KapeSkill spec to trigger the watch.
	patch := client.MergeFrom(skill.DeepCopy())
	skill.Spec.Instruction = "You are an expert analyst with domain expertise."
	require.NoError(t, k8sClient.Patch(ctx, skill, patch))

	// 6. Wait for the Deployment's rollout-hash to change.
	assert.Eventually(t, func() bool {
		var dep appsv1.Deployment
		if err := k8sClient.Get(ctx, depKey, &dep); err != nil {
			return false
		}
		newHash := dep.Annotations["kape.io/rollout-hash"]
		return newHash != "" && newHash != initialHash
	}, 10*time.Second, 200*time.Millisecond, "rollout-hash should change after KapeSkill spec update")
}

// staticConfigLoader is a test helper that returns a fixed KapeConfig.
type staticConfigLoader struct {
	cfg domainconfig.KapeConfig
}

func (l *staticConfigLoader) Load(_ context.Context) (domainconfig.KapeConfig, error) {
	return l.cfg, nil
}
```

**Build tag note:** The file uses `//go:build envtest`. Run it with:
```bash
KUBEBUILDER_ASSETS=$(setup-envtest use 1.32.0 --bin-dir /tmp/envtest-bins -p path) \
  go test -tags envtest ./operator/controller/... -run TestSkillWatch -v
```

The CRD path `../../infra/api/v1alpha1/crds` points to the generated CRD manifests installed by Slice 1. If that path differs in your layout, adjust accordingly — check where existing envtest harnesses load CRDs from (see `operator/cmd/playground/main.go` which uses `./crds`).

- [ ] **Step 2: Run the envtest test (requires KUBEBUILDER_ASSETS)**

```bash
export KUBEBUILDER_ASSETS=$(setup-envtest use 1.32.0 --bin-dir /tmp/envtest-bins -p path)
cd /home/tony/projects/kape-io && go test -tags envtest ./operator/controller/... -run TestSkillWatch_SpecChange_TriggersHandlerReconcile -v -timeout 60s 2>&1
```

Expected: `--- PASS: TestSkillWatch_SpecChange_TriggersHandlerReconcile`

If the test fails with "rollout-hash not changing", diagnose by checking whether the watch is firing:

```bash
# Check that KapeSkill is in the scheme — look for scheme registration in v1alpha1 groupversion_info.go
grep -n "KapeSkill" /home/tony/projects/kape-io/operator/infra/api/v1alpha1/groupversion_info.go

# Check that SetupHandlerReconciler actually includes the KapeSkill watch
grep -n "KapeSkill" /home/tony/projects/kape-io/operator/controller/handler.go
```

Common failure modes:
1. `KapeSkill` not registered in the scheme → `AddToScheme` must include it (Slice 1 registers it in `init()`; `v1alpha1.AddToScheme` covers it if Slice 1 is merged)
2. CRD path wrong → adjust `CRDDirectoryPaths` to match the actual crds directory
3. Handler reconciler fails deps gate → ensure the schema `Status.Conditions` update (line using `k8sClient.Status().Update`) was accepted by the status subresource

- [ ] **Step 3: Run all operator tests to confirm no regressions**

```bash
cd /home/tony/projects/kape-io && go test ./operator/... 2>&1
```

Expected: all non-envtest tests pass.

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice4-plan add operator/controller/watches_envtest_test.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice4-plan commit -m "test(operator): envtest — KapeSkill spec change triggers handler reconcile via watch"
```

---

## Task 4: Snyk Code scan on `operator/`

**Files:** No file changes — security scan only.

- [ ] **Step 1: Run `mcp__Snyk__snyk_code_scan` on the operator directory**

Call the `mcp__Snyk__snyk_code_scan` MCP tool with:
- `path`: `/home/tony/projects/kape-io/operator`

- [ ] **Step 2: Review results**

If issues are found in the newly introduced or modified files (`watches.go`, `handler.go`, `watches_test.go`, `watches_envtest_test.go`):
- Fix each issue in the affected file.
- Re-run the Snyk Code scan.
- Repeat until the scan reports no issues in the modified files.

If the scan reports only pre-existing issues in unchanged files, those are acceptable and do not block the PR.

- [ ] **Step 3: Confirm tests still pass after any fixes**

```bash
cd /home/tony/projects/kape-io && go test ./operator/... 2>&1
```

---

## Task 5: Push branch and open PR

**Files:** No code changes — Git + GitHub operations only.

- [ ] **Step 1: Push the branch**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice4-plan push -u origin docs/phase6-slice4-plan
```

- [ ] **Step 2: Run SBOM scans per CLAUDE.md checklist**

Call `mcp__Snyk__snyk_sbom_scan` three times with `format: cyclonedx1.4+json`:

1. `path`: `/home/tony/projects/kape-io/adapters`
2. `path`: `/home/tony/projects/kape-io/operator`
3. `path`: `/home/tony/projects/kape-io/task-service`

Record component counts and any flagged items from each result.

- [ ] **Step 3: Open the PR**

```bash
gh pr create \
  --title "feat(operator): slice 4 — KapeSkill cross-resource watch wiring" \
  --base main \
  --head docs/phase6-slice4-plan \
  --body "$(cat <<'EOF'
## Summary

- Adds `MapSkillToHandlers` mapper to `operator/controller/watches.go` — mirrors existing `MapToolToHandlers`, uses label selector `kape.io/skill-ref-{name}=true`
- Registers `KapeSkill` secondary watch in `SetupHandlerReconciler` alongside existing KapeTool and KapeSchema watches
- Adds unit tests for all mapper cases in `operator/controller/watches_test.go`
- Adds envtest test asserting KapeSkill spec change triggers rollout-hash annotation change on referencing handler Deployment

## Acceptance Criteria (from Phase 6 README)

> Editing a KapeSkill's spec triggers a reconcile of every referencing KapeHandler — verified by observing the rollout-hash annotation change on the handler's Deployment within one reconcile cycle.

Demonstrated by `TestSkillWatch_SpecChange_TriggersHandlerReconcile` (envtest, build tag `envtest`).

## Dependencies

- Slice 1: `KapeSkill` CRD types and `v1alpha1.AddToScheme` registration
- Slice 3: Writes `kape.io/skill-ref-{name}=true` labels on KapeHandler (this slice reads them via the mapper)

## Notes

- No label-writing in this slice — Slice 3 owns label sync (D7, D8 in spec decision log)
- The envtest test pre-applies the skill-ref label to simulate Slice 3's output

## Test Plan

- [ ] `go test ./operator/controller/... -run TestMapSkillToHandlers` — all 4 unit tests pass
- [ ] `go test -tags envtest ./operator/controller/... -run TestSkillWatch` — envtest test passes
- [ ] `go test ./operator/...` — full suite passes, no regressions
EOF
)"
```

- [ ] **Step 4: Post SBOM summary comment**

After the PR is created, post the SBOM summary comment:

```bash
gh pr comment "$(gh pr view --json url --jq '.url')" --body "$(cat <<'EOF'
## SBOM Summary

| Module | Components | Flagged |
|---|---|---|
| adapters | <count from scan> | <count or "none"> |
| operator | <count from scan> | <count or "none"> |
| task-service | <count from scan> | <count or "none"> |

Generated via Snyk CycloneDX 1.4 — <INSERT UTC TIMESTAMP e.g. 2026-05-09T10:00:00Z>
EOF
)"
```

Replace `<count from scan>` with the actual values from the Snyk SBOM scan results in Step 2. Replace `<INSERT UTC TIMESTAMP>` with the current UTC time.

---

## Definition of Done

- [ ] `MapSkillToHandlers` function exists in `operator/controller/watches.go` and follows the exact same pattern as `MapToolToHandlers`
- [ ] `SetupHandlerReconciler` in `operator/controller/handler.go` includes `.Watches(&v1alpha1.KapeSkill{}, handler.EnqueueRequestsFromMapFunc(MapSkillToHandlers(mgr.GetClient())))`
- [ ] All unit tests in `operator/controller/watches_test.go` pass
- [ ] Envtest test `TestSkillWatch_SpecChange_TriggersHandlerReconcile` passes: KapeSkill spec mutation → rollout-hash annotation on handler Deployment changes within one reconcile
- [ ] `go test ./operator/...` passes with no regressions
- [ ] Snyk Code scan on `operator/` shows no new issues in modified files
- [ ] PR raised with SBOM summary comment
