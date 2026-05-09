# Phase 6 Slice 3 — KapeHandlerReconciler Skill Gate + Tool Union + System Prompt + Lazy ConfigMap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `KapeHandlerReconciler` to (a) gate on `KapeSkill` readiness, (b) union skill-pulled tools with handler-direct tools into a deduplicated `toolMap`, (c) assemble the full system prompt (handler + eager skill instructions + lazy skill preamble) into `settings.toml`, (d) reconcile a `kape-skills-{handler-name}` ConfigMap when lazy skills exist (and delete it when none remain), (e) extend `computeRolloutHash` to cover sorted tool Specs + ordered skill Specs, (f) extend label sync with `kape.io/skill-ref-{name}=true` and `kape.io/tool-ref-{name}=true` for unioned tools, and (g) flip the `Ready` rollup to be forward-compatible (`Ready=True` iff no condition is explicitly `False`).

**Architecture:** Follows the established hexagonal pattern: pure reconcile logic in `controller/reconcile`, ports in `infra/ports`, k8s adapters in `infra/k8s`, TOML rendering in `infra/toml`. The `resolvedDependencies` struct (per spec §2.1) becomes the single carrier between dependency resolution and downstream steps (hash, deployment, prompt assembly). System prompt assembly is extracted into a pure function in a dedicated file so it stays test-friendly without entangling the reconcile loop. The lazy skill ConfigMap and its mount are gated on `len(lazySkills) > 0` — no ConfigMap, no mount when no lazy skills exist. Slice 3 must NOT add CEL admission rules (per spec D5) and must NOT mutate KapeSkill status (slice 1 owns it). Cross-resource watch wiring (slice 4) is explicitly out of scope; this slice only writes the labels slice 4 will read.

**Tech Stack:** Go 1.25, controller-runtime (sigs.k8s.io/controller-runtime), kubebuilder markers, controller-gen (via `make generate`), `pelletier/go-toml/v2`, envtest, testify, `crypto/sha256` + `encoding/json` (existing rollout hash machinery).

**Cross-slice contracts respected:**
- §2.1 `resolvedDependencies` schema and population order — implemented here
- §2.3 `DependenciesReady` reasons (adds `KapeSkillNotFound`, `KapeSkillNotReady`); `Ready` rollup negative form
- D7 (per-skill label) + D8 (transitive tool label) + D13 (`toolMap` keyed by `KapeTool.Name`, sorted iteration)
- D4: no `load_skill` runtime tool — Phase 6 mounts the ConfigMap as a forward-compatible affordance only

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| **Modify** | `operator/infra/api/v1alpha1/kapehandler_types.go` | Add `Skills []SkillRef` to `KapeHandlerSpec`; add `SkillRef` type |
| **Regenerate** | `operator/infra/api/v1alpha1/zz_generated.deepcopy.go` | DeepCopy regen via `make generate` |
| **Regenerate** | `crds/kape.io_kapehandlers.yaml` | CRD manifest regen via `make generate` |
| **Create** | `operator/infra/ports/skill_configmap.go` | `SkillConfigMapPort` interface |
| **Create** | `operator/infra/k8s/skill_configmap.go` | `SkillConfigMapAdapter` k8s adapter |
| **Modify** | `operator/infra/ports/handler.go` | (none — `HandlerRepository` unchanged in this slice) |
| **Modify** | `operator/infra/ports/skill.go` | (created in slice 1; no change) |
| **Create** | `operator/controller/reconcile/system_prompt.go` | Pure `AssembleSystemPrompt(handler, eagerSkills, lazySkills) string` |
| **Modify** | `operator/controller/reconcile/handler.go` | `resolvedDependencies` struct, extend `validateDependencies`, extend `computeRolloutHash`, add lazy ConfigMap step, extend label sync, fix `Ready` rollup, add `KapeSkillNotFound`/`KapeSkillNotReady` reason constants, add `SkillRepository` + `SkillConfigMapPort` to constructor |
| **Modify** | `operator/infra/toml/renderer.go` | Call `AssembleSystemPrompt`; pass through eager/lazy skill slices |
| **Modify** | `operator/infra/ports/handler.go` | Update `TOMLRenderer.Render` signature to accept `[]v1alpha1.KapeSkill` (eager + lazy) |
| **Modify** | `operator/infra/k8s/deployment.go` | Mount `/etc/kape/skills` from `kape-skills-{handler-name}` ConfigMap when caller signals lazy skills exist |
| **Modify** | `operator/infra/ports/handler.go` | Update `DeploymentPort.Ensure` signature to accept a `lazySkillsPresent bool` flag |
| **Modify** | `operator/controller/handler.go` | Pass `SkillRepository` + `SkillConfigMapAdapter` into `NewHandlerReconciler` |
| **Modify** | `operator/cmd/main.go` | Wire the new constructor args (`SkillRepository`, `SkillConfigMapAdapter`) |
| **Create** | `operator/controller/reconcile/system_prompt_test.go` | Unit tests for `AssembleSystemPrompt` |
| **Modify** | `operator/controller/reconcile/handler_test.go` | Add skill-gate, lazy/eager mix, hash-stability, label-sync, lazy ConfigMap lifecycle tests |
| **Modify** | `operator/infra/toml/renderer_test.go` | Extend with eager-skill ordering + lazy preamble + skill-less tests |

> Three port definitions live in the same file (`operator/infra/ports/handler.go`). Touch the file once with all three changes (skill repo wiring is via `ports/skill.go` from slice 1 — no edit here).

---

## Task 1: Add `Skills []SkillRef` to `KapeHandlerSpec`

**Files:**
- Modify: `operator/infra/api/v1alpha1/kapehandler_types.go`

- [ ] **Step 1: Add `SkillRef` type and `Skills` field**

Open `operator/infra/api/v1alpha1/kapehandler_types.go` and apply the following edits:

After the `ToolRef` type definition (around line 94), add:

```go
// SkillRef references a KapeSkill available to the agent.
type SkillRef struct {
	// Ref is the name of the KapeSkill in the same namespace.
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`
}
```

In `KapeHandlerSpec` (around line 158, after the `Tools []ToolRef` field), add:

```go
	// Skills is the list of KapeSkills this handler attaches.
	// Each skill's instruction is assembled into the system prompt (eager) or
	// mounted at /etc/kape/skills (lazy). Skills are processed in declaration order.
	// +optional
	Skills []SkillRef `json:"skills,omitempty"`
```

- [ ] **Step 2: Verify the file compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./infra/api/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/infra/api/v1alpha1/kapehandler_types.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(api): add KapeHandlerSpec.skills + SkillRef type"
```

---

## Task 2: Regenerate DeepCopy and CRD manifest

**Files:**
- Regenerate: `operator/infra/api/v1alpha1/zz_generated.deepcopy.go`
- Regenerate: `crds/kape.io_kapehandlers.yaml`

- [ ] **Step 1: Run code generation**

```bash
cd /home/tony/projects/kape-io && make generate
```

Expected: `zz_generated.deepcopy.go` updated to include `SkillRef.DeepCopyInto`, `KapeHandlerSpec.Skills` deep copy, and `crds/kape.io_kapehandlers.yaml` updated to include `spec.properties.skills` schema.

- [ ] **Step 2: Verify generated files compile**

```bash
cd /home/tony/projects/kape-io/operator && go build ./...
```

Expected: no errors.

- [ ] **Step 3: Inspect the generated CRD manifest**

```bash
grep -A 12 "skills:" /home/tony/projects/kape-io/crds/kape.io_kapehandlers.yaml | head -20
```

Expected output includes a `skills:` array with `items.properties.ref` (string, minLength: 1).

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/infra/api/v1alpha1/zz_generated.deepcopy.go crds/kape.io_kapehandlers.yaml
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "chore(generate): regen deepcopy + CRD manifest for KapeHandler.skills"
```

---

## Task 3: Define `SkillConfigMapPort` interface

**Files:**
- Create: `operator/infra/ports/skill_configmap.go`

- [ ] **Step 1: Write the port file**

```go
package ports

import (
	"context"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// SkillConfigMapPort manages the kape-skills-{handler-name} ConfigMap that
// holds one file per lazy KapeSkill instruction. The ConfigMap is created
// when at least one lazy skill is present and deleted when none remain.
type SkillConfigMapPort interface {
	// Ensure creates or patches the kape-skills-{handler-name} ConfigMap with
	// one data entry per lazy skill keyed as "{skill-name}.txt".
	Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, lazySkills []v1alpha1.KapeSkill) error

	// Delete removes the kape-skills-{handler-name} ConfigMap. Returns nil when
	// the ConfigMap does not exist.
	Delete(ctx context.Context, handler *v1alpha1.KapeHandler) error
}
```

- [ ] **Step 2: Verify the file compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./infra/ports/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/infra/ports/skill_configmap.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(ports): add SkillConfigMapPort for kape-skills-{handler} ConfigMap"
```

---

## Task 4: Implement `SkillConfigMapAdapter` (k8s)

**Files:**
- Create: `operator/infra/k8s/skill_configmap.go`

- [ ] **Step 1: Write the adapter**

```go
package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// SkillConfigMapAdapter implements ports.SkillConfigMapPort.
type SkillConfigMapAdapter struct {
	client client.Client
}

// NewSkillConfigMapAdapter creates a SkillConfigMapAdapter.
func NewSkillConfigMapAdapter(c client.Client) *SkillConfigMapAdapter {
	return &SkillConfigMapAdapter{client: c}
}

// SkillConfigMapName is the name of the kape-skills ConfigMap for a handler.
func SkillConfigMapName(handlerName string) string {
	return "kape-skills-" + handlerName
}

// Ensure creates or patches the kape-skills-{handler-name} ConfigMap.
// Each lazy skill becomes one data entry keyed "{skill-name}.txt" with the raw
// (unrendered) instruction as the value. Template variable resolution is
// deferred to the handler runtime per spec 0013 §2.4.
func (a *SkillConfigMapAdapter) Ensure(ctx context.Context, handler *v1alpha1.KapeHandler, lazySkills []v1alpha1.KapeSkill) error {
	name := SkillConfigMapName(handler.Name)
	key := types.NamespacedName{Name: name, Namespace: handler.Namespace}

	data := make(map[string]string, len(lazySkills))
	for _, s := range lazySkills {
		data[s.Name+".txt"] = s.Spec.Instruction
	}

	desired := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: handler.Namespace,
			Labels: map[string]string{
				"kape.io/handler":              handler.Name,
				"app.kubernetes.io/managed-by": "kape-operator",
				"app.kubernetes.io/name":       name,
			},
		},
		Data: data,
	}
	setOwnerRef(handler, &desired.ObjectMeta)

	var existing corev1.ConfigMap
	err := a.client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		return a.client.Create(ctx, &desired)
	}
	if err != nil {
		return fmt.Errorf("getting ConfigMap %s/%s: %w", handler.Namespace, name, err)
	}

	patch := client.MergeFrom(existing.DeepCopy())
	existing.Data = desired.Data
	existing.Labels = desired.Labels
	existing.OwnerReferences = desired.OwnerReferences
	return a.client.Patch(ctx, &existing, patch)
}

// Delete removes the kape-skills-{handler-name} ConfigMap. Returns nil if not found.
func (a *SkillConfigMapAdapter) Delete(ctx context.Context, handler *v1alpha1.KapeHandler) error {
	name := SkillConfigMapName(handler.Name)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: handler.Namespace},
	}
	if err := a.client.Delete(ctx, cm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("deleting ConfigMap %s/%s: %w", handler.Namespace, name, err)
	}
	return nil
}
```

- [ ] **Step 2: Verify the adapter compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./infra/k8s/...
```

Expected: no errors.

- [ ] **Step 3: Verify it satisfies the port interface**

Add a temporary file `operator/infra/k8s/skill_configmap_iface_test.go` (delete in Step 5):

```go
package k8s

import "github.com/kape-io/kape/operator/infra/ports"

var _ ports.SkillConfigMapPort = (*SkillConfigMapAdapter)(nil)
```

Run:

```bash
cd /home/tony/projects/kape-io/operator && go vet ./infra/k8s/...
```

Expected: no errors.

- [ ] **Step 4: Remove the temporary interface check**

```bash
rm /home/tony/projects/kape-io/operator/infra/k8s/skill_configmap_iface_test.go
```

(Production code already implements the interface — the temp file was only for verification.)

- [ ] **Step 5: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/infra/k8s/skill_configmap.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(k8s): SkillConfigMapAdapter for lazy skill files"
```

---

## Task 5: Update `TOMLRenderer.Render` signature to accept skills

**Files:**
- Modify: `operator/infra/ports/handler.go`

- [ ] **Step 1: Edit the `TOMLRenderer` interface**

Open `operator/infra/ports/handler.go` and replace the current `TOMLRenderer` interface (around lines 45-52) with:

```go
// TOMLRenderer produces a settings.toml string from a KapeHandler, its resolved
// schema, resolved tools, eager + lazy skill slices, and platform config.
//
// The renderer is responsible for assembling the full system_prompt
// (handler.systemPrompt → eager skill instructions in declaration order →
// lazy skill preamble) per spec 0013 §3.2.
type TOMLRenderer interface {
	Render(
		handler *v1alpha1.KapeHandler,
		schema *v1alpha1.KapeSchema,
		tools []v1alpha1.KapeTool,
		eagerSkills []v1alpha1.KapeSkill,
		lazySkills []v1alpha1.KapeSkill,
		cfg domainconfig.KapeConfig,
	) (string, error)
}
```

- [ ] **Step 2: Verify the port still compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./infra/ports/...
```

Expected: no errors.

(The renderer implementation in `operator/infra/toml/renderer.go` will be updated in Task 8 — the build of `./...` will fail until then. That's expected.)

- [ ] **Step 3: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/infra/ports/handler.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "refactor(ports): TOMLRenderer.Render accepts eager+lazy skill slices"
```

---

## Task 6: Update `DeploymentPort.Ensure` signature to accept lazy-skills flag

**Files:**
- Modify: `operator/infra/ports/handler.go`

- [ ] **Step 1: Edit the `DeploymentPort` interface**

Replace the current `DeploymentPort` interface (around lines 30-36) with:

```go
// DeploymentPort manages the handler Deployment.
type DeploymentPort interface {
	// Ensure creates or patches the handler Deployment with sidecar injection
	// for mcp-type tools. lazySkillsPresent=true causes the kape-skills volume
	// + /etc/kape/skills mount to be added; the ConfigMap itself is owned by
	// SkillConfigMapPort.
	Ensure(
		ctx context.Context,
		handler *v1alpha1.KapeHandler,
		cfg domainconfig.KapeConfig,
		rolloutHash string,
		tools []v1alpha1.KapeTool,
		lazySkillsPresent bool,
	) error

	// GetStatus reads the current Deployment status. found is false when the Deployment does not exist.
	GetStatus(ctx context.Context, key types.NamespacedName) (status *appsv1.DeploymentStatus, found bool, err error)
}
```

- [ ] **Step 2: Verify the port compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./infra/ports/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/infra/ports/handler.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "refactor(ports): DeploymentPort.Ensure accepts lazySkillsPresent flag"
```

---

## Task 7: Implement `AssembleSystemPrompt` (pure function)

**Files:**
- Create: `operator/controller/reconcile/system_prompt.go`

- [ ] **Step 1: Write the pure function**

```go
package reconcile

import (
	"strings"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

const skillSeparator = "\n\n---\n\n"

// AssembleSystemPrompt builds the final system prompt per spec 0013 §3.2.
//
// Layout:
//
//	1. handler.spec.llm.systemPrompt (verbatim)
//	2. "\n\n---\n\n"  -- only if eagerSkills is non-empty
//	3. eager skill instructions joined by "\n\n---\n\n", in slice order
//	4. "\n\n---\n\n"  -- only if both eager and lazy skills exist
//	   (or "\n\n" if only lazy exists)
//	5. lazy skill preamble — only if lazySkills is non-empty
//
// The render context (Jinja2 variables inside instructions) is resolved at
// handler runtime per spec 0013 §2.3 — this function treats instructions as
// opaque strings.
//
// AssembleSystemPrompt is pure: same inputs always produce the same output,
// no I/O, no time/randomness. Test exhaustively in system_prompt_test.go.
func AssembleSystemPrompt(handler *v1alpha1.KapeHandler, eagerSkills []v1alpha1.KapeSkill, lazySkills []v1alpha1.KapeSkill) string {
	var b strings.Builder
	b.WriteString(handler.Spec.LLM.SystemPrompt)

	if len(eagerSkills) > 0 {
		b.WriteString(skillSeparator)
		eagerInstructions := make([]string, 0, len(eagerSkills))
		for _, s := range eagerSkills {
			eagerInstructions = append(eagerInstructions, s.Spec.Instruction)
		}
		b.WriteString(strings.Join(eagerInstructions, skillSeparator))
	}

	if len(lazySkills) > 0 {
		if len(eagerSkills) > 0 {
			b.WriteString(skillSeparator)
		} else {
			b.WriteString("\n\n")
		}
		b.WriteString(buildLazyPreamble(lazySkills))
	}

	return b.String()
}

// buildLazyPreamble returns the operator-injected text that lists every lazy
// skill by name + description. Per spec 0013 §2.4 the closing line guides the
// agent to call load_skill with one of the listed names.
func buildLazyPreamble(lazySkills []v1alpha1.KapeSkill) string {
	var b strings.Builder
	b.WriteString("Available skills (call load_skill with the skill name to retrieve full instructions):\n")
	for _, s := range lazySkills {
		b.WriteString("- ")
		b.WriteString(s.Name)
		b.WriteString(": ")
		b.WriteString(s.Spec.Description)
		b.WriteString("\n")
	}
	b.WriteString("\nWhen you determine a skill is relevant, call load_skill with its name before proceeding.")
	return b.String()
}
```

- [ ] **Step 2: Verify the file compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./controller/reconcile/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/controller/reconcile/system_prompt.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(reconcile): pure AssembleSystemPrompt for handler+eager+lazy skills"
```

---

## Task 8: Update `toml.Renderer.Render` to call `AssembleSystemPrompt`

**Files:**
- Modify: `operator/infra/toml/renderer.go`

- [ ] **Step 1: Add an import for the reconcile package**

At the top of `operator/infra/toml/renderer.go`, add to the import block:

```go
	reconcile "github.com/kape-io/kape/operator/controller/reconcile"
```

(If you prefer to avoid the controller→infra package coupling, see Task 8 alternative at end of this task. The default approach is to import — `reconcile` already imports `infra/ports` and `infra/api/v1alpha1`, so this is one-directional and breaks no boundary.)

- [ ] **Step 2: Update `Render` signature and call sites**

Replace the existing `Render` method (lines 23-88) with:

```go
// Render serialises a KapeHandler, its resolved KapeSchema, resolved KapeTools,
// eager + lazy skill slices, and platform config into a settings.toml string.
//
// The full system_prompt is assembled per spec 0013 §3.2 by calling
// reconcile.AssembleSystemPrompt — handler.systemPrompt → eager skill
// instructions → lazy skill preamble.
func (r *Renderer) Render(
	handler *v1alpha1.KapeHandler,
	schema *v1alpha1.KapeSchema,
	tools []v1alpha1.KapeTool,
	eagerSkills []v1alpha1.KapeSkill,
	lazySkills []v1alpha1.KapeSkill,
	cfg domainconfig.KapeConfig,
) (string, error) {
	cfg = cfg.WithDefaults()

	replayOnStartup := true
	if handler.Spec.Trigger.ReplayOnStartup != nil {
		replayOnStartup = *handler.Spec.Trigger.ReplayOnStartup
	}
	maxIterations := handler.Spec.LLM.MaxIterations
	if maxIterations == 0 {
		maxIterations = cfg.DefaultMaxIterations
	}

	consumerName := strings.ReplaceAll(handler.Spec.Trigger.Type, ".", "-")
	taskServiceEndpoint := fmt.Sprintf("http://kape-task-service.%s:8080", handler.Namespace)

	actions, err := buildActions(handler)
	if err != nil {
		return "", fmt.Errorf("building actions: %w", err)
	}

	toolSections := buildToolSections(handler, tools)

	schemaSection, err := buildSchemaSection(schema)
	if err != nil {
		return "", fmt.Errorf("building schema section: %w", err)
	}

	systemPrompt := reconcile.AssembleSystemPrompt(handler, eagerSkills, lazySkills)

	s := settingsTOML{
		Kape: kapeTOML{
			HandlerName:        handler.Name,
			HandlerNamespace:   handler.Namespace,
			ClusterName:        cfg.ClusterName,
			DryRun:             handler.Spec.DryRun,
			MaxIterations:      maxIterations,
			SchemaName:         handler.Spec.SchemaRef,
			ReplayOnStartup:    replayOnStartup,
			MaxEventAgeSeconds: handler.Spec.Trigger.MaxEventAgeSeconds,
		},
		LLM: llmTOML{
			Provider:     handler.Spec.LLM.Provider,
			Model:        handler.Spec.LLM.Model,
			SystemPrompt: systemPrompt,
		},
		NATS: natsTOML{
			Subject:  handler.Spec.Trigger.Type,
			Consumer: consumerName,
			Stream:   "kape-events",
		},
		TaskService: taskServiceTOML{Endpoint: taskServiceEndpoint},
		OTEL:        otelTOML{Endpoint: "http://otel-collector.kape-system:4318", ServiceName: "kape-handler"},
		Tools:       toolSections,
		Schema:      schemaSection,
		Actions:     actions,
	}

	var buf bytes.Buffer
	if err := gotoml.NewEncoder(&buf).Encode(s); err != nil {
		return "", fmt.Errorf("encoding settings.toml: %w", err)
	}
	return buf.String(), nil
}
```

> **If the import-cycle alternative is needed:** if `controller/reconcile` ever imports `infra/toml` in future, this circular dep will break. Mitigation if it ever happens: move `AssembleSystemPrompt` and `buildLazyPreamble` into a third-party-free package such as `operator/domain/systemprompt/`. Slice 3 does NOT take this path because `controller/reconcile` does not import `infra/toml` today (it depends on `ports.TOMLRenderer`).

- [ ] **Step 3: Verify the renderer compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./infra/toml/...
```

Expected: no errors. If you see an import cycle, switch to the alternative described above.

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/infra/toml/renderer.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(toml): renderer assembles system_prompt from handler+skills"
```

---

## Task 9: Update `DeploymentAdapter` to mount `/etc/kape/skills` when lazy skills present

**Files:**
- Modify: `operator/infra/k8s/deployment.go`

- [ ] **Step 1: Update `Ensure` to accept the new flag**

In `operator/infra/k8s/deployment.go`, replace the `Ensure` method (lines 33-57) with:

```go
// Ensure creates or patches the handler Deployment with sidecar injection for mcp-type tools.
func (a *DeploymentAdapter) Ensure(
	ctx context.Context,
	handler *v1alpha1.KapeHandler,
	cfg domainconfig.KapeConfig,
	rolloutHash string,
	tools []v1alpha1.KapeTool,
	lazySkillsPresent bool,
) error {
	name := deploymentName(handler.Name)
	key := types.NamespacedName{Name: name, Namespace: handler.Namespace}
	desired := buildDeployment(handler, cfg, rolloutHash, tools, lazySkillsPresent)

	var existing appsv1.Deployment
	err := a.client.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		return a.client.Create(ctx, &desired)
	}
	if err != nil {
		return fmt.Errorf("getting Deployment %s/%s: %w", handler.Namespace, name, err)
	}
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Spec = desired.Spec
	existing.Annotations = desired.Annotations
	existing.Labels = desired.Labels
	return a.client.Patch(ctx, &existing, patch)
}
```

- [ ] **Step 2: Update `buildDeployment` to plumb the flag**

Replace `buildDeployment` (lines 71-145) with:

```go
func buildDeployment(handler *v1alpha1.KapeHandler, cfg domainconfig.KapeConfig, rolloutHash string, tools []v1alpha1.KapeTool, lazySkillsPresent bool) appsv1.Deployment {
	cfg = cfg.WithDefaults()
	name := deploymentName(handler.Name)
	saName := serviceAccountName(handler.Name)
	cmName := configMapName(handler.Name)
	noAutoMount := false

	var replicas int32 = 1
	if handler.Spec.Scaling != nil && handler.Spec.Scaling.MinReplicas > 0 {
		replicas = handler.Spec.Scaling.MinReplicas
	}

	envVars := []corev1.EnvVar{
		{Name: "KAPE_HANDLER_NAME", Value: handler.Name},
		{Name: "KAPE_NAMESPACE", Value: handler.Namespace},
	}
	envVars = append(envVars, handler.Spec.Envs...)

	handlerVolumeMounts := []corev1.VolumeMount{{
		Name:      "settings",
		MountPath: "/etc/kape",
		ReadOnly:  true,
	}}
	volumes := []corev1.Volume{{
		Name: "settings",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
			},
		},
	}}

	if lazySkillsPresent {
		handlerVolumeMounts = append(handlerVolumeMounts, corev1.VolumeMount{
			Name:      "kape-skills",
			MountPath: "/etc/kape/skills",
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "kape-skills",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: SkillConfigMapName(handler.Name)},
				},
			},
		})
	}

	handlerContainer := corev1.Container{
		Name:         "handler",
		Image:        cfg.HandlerImageRef(),
		Env:          envVars,
		Resources:    resolveHandlerResources(handler.Spec.Resources),
		VolumeMounts: handlerVolumeMounts,
	}

	containers := append([]corev1.Container{handlerContainer}, buildSidecars(handler, tools, cfg)...)

	dep := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: handler.Namespace,
			Labels: map[string]string{
				"kape.io/handler":              handler.Name,
				"app.kubernetes.io/managed-by": "kape-operator",
				"app.kubernetes.io/name":       name,
			},
			Annotations: map[string]string{"kape.io/rollout-hash": rolloutHash},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kape.io/handler": handler.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"kape.io/handler":        handler.Name,
						"app.kubernetes.io/name": name,
					},
					Annotations: map[string]string{"kape.io/rollout-hash": rolloutHash},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           saName,
					AutomountServiceAccountToken: &noAutoMount,
					Containers:                   containers,
					Volumes:                      volumes,
				},
			},
		},
	}
	setOwnerRef(handler, &dep.ObjectMeta)
	return dep
}
```

- [ ] **Step 3: Verify the adapter compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./infra/k8s/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/infra/k8s/deployment.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(k8s): mount kape-skills volume on handler when lazy skills present"
```

---

## Task 10: Add `resolvedDependencies` struct + reason constants in `handler.go`

**Files:**
- Modify: `operator/controller/reconcile/handler.go`

- [ ] **Step 1: Add reason constants**

At the top of `operator/controller/reconcile/handler.go`, immediately after the imports block (above `type HandlerReconciler struct`), add:

```go
// Reason constants for the DependenciesReady condition. The Ready rollup
// (see buildHandlerConditions) is the negative form: Ready=True iff no
// condition is explicitly False, which is forward-compatible with the
// KapeProxyReady condition slice 6 will introduce.
const (
	ReasonReady               = "Ready"
	ReasonKapeSchemaInvalid   = "KapeSchemaInvalid"
	ReasonKapeToolNotReady    = "KapeToolNotReady"
	ReasonKapeSkillNotFound   = "KapeSkillNotFound"
	ReasonKapeSkillNotReady   = "KapeSkillNotReady"
)
```

- [ ] **Step 2: Add the `resolvedDependencies` struct**

Below the constants, add:

```go
// resolvedDependencies is the carrier between dependency resolution and the
// downstream reconcile steps (rollout hash, deployment, system prompt, lazy
// ConfigMap, label sync). Per spec §2.1 the contract is:
//
//   - Schema:  the Ready KapeSchema referenced by handler.spec.schemaRef
//   - Tools:   sorted slice of every KapeTool in the unioned toolMap
//              (by KapeTool.Name) — used for deterministic hashing and
//              settings.toml [tools.*] section emission
//   - Skills:  every KapeSkill from handler.spec.skills[] in declaration
//              order — used for hash (D13) and system prompt assembly
//   - ToolMap: keyed by KapeTool.Name (D13); union of handler-direct tools
//              and skill-pulled tools; downstream consumers iterate Tools
//              for deterministic order, ToolMap for O(1) lookup
type resolvedDependencies struct {
	Schema  *v1alpha1.KapeSchema
	Tools   []v1alpha1.KapeTool
	Skills  []v1alpha1.KapeSkill
	ToolMap map[string]v1alpha1.KapeTool
}

// EagerSkills returns skills with LazyLoad=false in declaration order.
func (d *resolvedDependencies) EagerSkills() []v1alpha1.KapeSkill {
	out := make([]v1alpha1.KapeSkill, 0, len(d.Skills))
	for _, s := range d.Skills {
		if !s.Spec.LazyLoad {
			out = append(out, s)
		}
	}
	return out
}

// LazySkills returns skills with LazyLoad=true in declaration order.
func (d *resolvedDependencies) LazySkills() []v1alpha1.KapeSkill {
	out := make([]v1alpha1.KapeSkill, 0, len(d.Skills))
	for _, s := range d.Skills {
		if s.Spec.LazyLoad {
			out = append(out, s)
		}
	}
	return out
}
```

- [ ] **Step 3: Verify the file compiles (the new type is unused — that's fine for now)**

```bash
cd /home/tony/projects/kape-io/operator && go vet ./controller/reconcile/...
```

Expected: no errors. (`go vet` does not complain about unused types; `go build` will only complain if something is broken.)

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/controller/reconcile/handler.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(reconcile): resolvedDependencies struct + reason constants"
```

---

## Task 11: Add `SkillRepository` + `SkillConfigMapPort` to `HandlerReconciler` constructor

**Files:**
- Modify: `operator/controller/reconcile/handler.go`

- [ ] **Step 1: Extend the struct and constructor**

Replace the `HandlerReconciler` struct (lines 21-31) and `NewHandlerReconciler` (lines 41-64) with:

```go
// HandlerReconciler performs the full reconcile logic for KapeHandler.
type HandlerReconciler struct {
	handlers         ports.HandlerRepository
	schemas          ports.SchemaRepository
	tools            ports.ToolRepository
	skills           ports.SkillRepository
	configMaps       ports.ConfigMapPort
	skillConfigMaps  ports.SkillConfigMapPort
	serviceAccounts  ports.ServiceAccountPort
	deployments      ports.DeploymentPort
	scaledObjects    ports.ScaledObjectPort
	tomlRenderer     ports.TOMLRenderer
	kapeConfig       ports.KapeConfigLoader
}

// NewHandlerReconciler creates a HandlerReconciler with all required dependencies.
func NewHandlerReconciler(
	handlers ports.HandlerRepository,
	schemas ports.SchemaRepository,
	tools ports.ToolRepository,
	skills ports.SkillRepository,
	configMaps ports.ConfigMapPort,
	skillConfigMaps ports.SkillConfigMapPort,
	serviceAccounts ports.ServiceAccountPort,
	deployments ports.DeploymentPort,
	scaledObjects ports.ScaledObjectPort,
	tomlRenderer ports.TOMLRenderer,
	kapeConfig ports.KapeConfigLoader,
) *HandlerReconciler {
	return &HandlerReconciler{
		handlers:        handlers,
		schemas:         schemas,
		tools:           tools,
		skills:          skills,
		configMaps:      configMaps,
		skillConfigMaps: skillConfigMaps,
		serviceAccounts: serviceAccounts,
		deployments:     deployments,
		scaledObjects:   scaledObjects,
		tomlRenderer:    tomlRenderer,
		kapeConfig:      kapeConfig,
	}
}
```

> **Why a single struct field per port** — not a config struct: matches the existing pattern. If we later refactor to a `ReconcilerDeps` struct we should do it as a separate refactor PR, not bundled with slice 3.

- [ ] **Step 2: Verify the file compiles** (callers haven't been updated yet — `go build ./...` will fail; `go build ./controller/reconcile/...` should still succeed because the constructor's references resolve)

```bash
cd /home/tony/projects/kape-io/operator && go build ./controller/reconcile/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/controller/reconcile/handler.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "refactor(reconcile): inject SkillRepository + SkillConfigMapPort into HandlerReconciler"
```

---

## Task 12: Extend `validateDependencies` to resolve skills + union toolMap

**Files:**
- Modify: `operator/controller/reconcile/handler.go`

- [ ] **Step 1: Add a `unionToolMap` helper above `validateDependencies`**

Insert this helper just above the existing `validateDependencies` function:

```go
// unionToolMap inserts a tool into the map keyed by tool.Name. Subsequent
// inserts of the same name are no-ops per spec D13 (KapeTool name uniqueness
// makes overwrite semantically equivalent).
func unionToolMap(m map[string]v1alpha1.KapeTool, tool v1alpha1.KapeTool) {
	if _, ok := m[tool.Name]; ok {
		return
	}
	m[tool.Name] = tool
}

// sortedToolsByName returns the values of a toolMap as a slice sorted by Name.
// Sorting is required for hash stability per spec §2.1 (line 4 of population
// order) and for deterministic settings.toml output.
func sortedToolsByName(m map[string]v1alpha1.KapeTool) []v1alpha1.KapeTool {
	out := make([]v1alpha1.KapeTool, 0, len(m))
	for _, t := range m {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
```

Add `"sort"` to the imports block at the top of the file (alphabetical order, after `"fmt"`).

- [ ] **Step 2: Replace `validateDependencies` with the extended version**

Replace the existing `validateDependencies` function (lines 200-243) with:

```go
// validateDependencies checks KapeSchema + KapeTool + KapeSkill readiness.
// Returns a fully resolved resolvedDependencies on success. On any not-ready
// dependency, returns ready=false with reason from the constants block.
//
// Population order per spec §2.1:
//
//	1. handler.spec.tools[]   → toolMap[tool.Name] = tool
//	2. handler.spec.skills[]  → fetch each KapeSkill, gate on Ready
//	3. each skill.spec.tools[] → fetch each KapeTool, gate on Ready,
//	                              union into toolMap
//	4. sort toolMap values by Name → deps.Tools (for hash stability)
//
// Skills slice keeps declaration order (NOT sorted) per D13: reordering
// handler.spec.skills changes prompt assembly and must change the hash.
func (r *HandlerReconciler) validateDependencies(ctx context.Context, handler *v1alpha1.KapeHandler) (
	deps *resolvedDependencies,
	ready bool,
	message, reason string,
	err error,
) {
	// (1) Schema
	schemaKey := types.NamespacedName{Name: handler.Spec.SchemaRef, Namespace: handler.Namespace}
	schema, err := r.schemas.Get(ctx, schemaKey)
	if err != nil {
		return nil, false, "", "", fmt.Errorf("fetching KapeSchema: %w", err)
	}
	if schema == nil || !isConditionTrue(schema.Status.Conditions, "Ready") {
		msg := fmt.Sprintf("KapeSchema %q not found or not ready", handler.Spec.SchemaRef)
		if schema != nil {
			if c := findCond(schema.Status.Conditions, "Ready"); c != nil && c.Message != "" {
				msg = c.Message
			}
		}
		return nil, false, msg, ReasonKapeSchemaInvalid, nil
	}

	toolMap := make(map[string]v1alpha1.KapeTool)

	// (2) Handler-direct tools
	for _, ref := range handler.Spec.Tools {
		toolKey := types.NamespacedName{Name: ref.Ref, Namespace: handler.Namespace}
		tool, err := r.tools.Get(ctx, toolKey)
		if err != nil {
			return nil, false, "", "", fmt.Errorf("fetching KapeTool %q: %w", ref.Ref, err)
		}
		if tool == nil || !isConditionTrue(tool.Status.Conditions, "Ready") {
			msg := fmt.Sprintf("KapeTool %q not found or not ready", ref.Ref)
			if tool != nil {
				if c := findCond(tool.Status.Conditions, "Ready"); c != nil && c.Message != "" {
					msg = fmt.Sprintf("KapeTool %q: %s", ref.Ref, c.Message)
				}
			}
			return nil, false, msg, ReasonKapeToolNotReady, nil
		}
		unionToolMap(toolMap, *tool)
	}

	// (3) Skills + skill-pulled tools
	skillsList := make([]v1alpha1.KapeSkill, 0, len(handler.Spec.Skills))
	for _, ref := range handler.Spec.Skills {
		skillKey := types.NamespacedName{Name: ref.Ref, Namespace: handler.Namespace}
		skill, err := r.skills.Get(ctx, skillKey)
		if err != nil {
			return nil, false, "", "", fmt.Errorf("fetching KapeSkill %q: %w", ref.Ref, err)
		}
		if skill == nil {
			return nil, false, fmt.Sprintf("KapeSkill %q not found", ref.Ref), ReasonKapeSkillNotFound, nil
		}
		if !isConditionTrue(skill.Status.Conditions, "Ready") {
			msg := fmt.Sprintf("KapeSkill %q not ready", ref.Ref)
			if c := findCond(skill.Status.Conditions, "Ready"); c != nil && c.Message != "" {
				msg = fmt.Sprintf("KapeSkill %q: %s", ref.Ref, c.Message)
			}
			return nil, false, msg, ReasonKapeSkillNotReady, nil
		}

		// Skill-pulled tools
		for _, sToolRef := range skill.Spec.Tools {
			toolKey := types.NamespacedName{Name: sToolRef.Ref, Namespace: handler.Namespace}
			tool, err := r.tools.Get(ctx, toolKey)
			if err != nil {
				return nil, false, "", "", fmt.Errorf("fetching KapeTool %q (via skill %q): %w", sToolRef.Ref, ref.Ref, err)
			}
			if tool == nil || !isConditionTrue(tool.Status.Conditions, "Ready") {
				msg := fmt.Sprintf("KapeSkill %q: KapeTool %q not Ready", ref.Ref, sToolRef.Ref)
				return nil, false, msg, ReasonKapeSkillNotReady, nil
			}
			unionToolMap(toolMap, *tool)
		}
		skillsList = append(skillsList, *skill)
	}

	deps = &resolvedDependencies{
		Schema:  schema,
		Tools:   sortedToolsByName(toolMap),
		Skills:  skillsList,
		ToolMap: toolMap,
	}
	return deps, true, "", "", nil
}
```

- [ ] **Step 3: Verify the file compiles (existing `Reconcile` body still references the old return shape — that's expected; we'll fix it in Task 14)**

```bash
cd /home/tony/projects/kape-io/operator && go build ./controller/reconcile/...
```

Expected: errors about `Reconcile` calling the old signature. Move on to Task 13 — we'll fix `Reconcile` after extending `computeRolloutHash` so the diff is reviewable.

> **NOTE:** The repo's standard practice is to keep the build green after every task. To preserve that, do Tasks 12+13+14 as one logical unit before pushing — but commit each individually. The build is green after Task 14.

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/controller/reconcile/handler.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(reconcile): validateDependencies returns resolvedDependencies; resolves skills + skill-pulled tools"
```

---

## Task 13: Extend `computeRolloutHash` to cover sorted tools + ordered skills

**Files:**
- Modify: `operator/controller/reconcile/handler.go`

- [ ] **Step 1: Replace `computeRolloutHash`**

Replace the existing function (lines 245-262) with:

```go
// computeRolloutHash hashes (in this fixed order, per spec §2.1):
//
//	1. handler.Spec
//	2. schema.Spec
//	3. for each tool in deps.Tools (sorted by Name): tool.Spec
//	4. for each skill in deps.Skills (declaration order): skill.Spec
//
// Slice 5 will append cfg.KapeproxyImage + cfg.KapeproxyImageVersion in
// positions 5+6. Do NOT add those here — the slice 5 PR is the single place
// kape-config kapeproxy fields are introduced.
//
// Skills are NOT sorted (D13): reordering handler.spec.skills[] changes the
// system prompt assembly order, so the hash must reflect order, not just
// set membership.
func computeRolloutHash(handler *v1alpha1.KapeHandler, deps *resolvedDependencies) (string, error) {
	h := sha256.New()
	for _, item := range []interface{}{handler.Spec, deps.Schema.Spec} {
		b, err := json.Marshal(item)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	for _, t := range deps.Tools {
		b, err := json.Marshal(t.Spec)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	for _, s := range deps.Skills {
		b, err := json.Marshal(s.Spec)
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
```

- [ ] **Step 2: Verify the function compiles in isolation** (callers in `Reconcile` still pass old args — fix in Task 14)

```bash
cd /home/tony/projects/kape-io/operator && go build ./controller/reconcile/...
```

Expected: errors about the call site in `Reconcile` — that's expected; Task 14 fixes them.

- [ ] **Step 3: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/controller/reconcile/handler.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(reconcile): computeRolloutHash includes sorted tools + ordered skills"
```

---

## Task 14: Update `Reconcile` to use `resolvedDependencies`, add lazy ConfigMap step, extend label sync, fix `Ready` rollup

**Files:**
- Modify: `operator/controller/reconcile/handler.go`

- [ ] **Step 1: Replace the `Reconcile` method**

Replace the entire `Reconcile` method (lines 67-197) with:

```go
// Reconcile implements the full KapeHandler reconcile loop:
//
//   1. Fetch KapeHandler
//   2. Validate dependencies (schema + tools + skills + skill-pulled tools)
//   3. Validate scaling
//   4. Compute rollout hash (handler + schema + sorted tools + ordered skills)
//   5. Render settings.toml + ensure ConfigMap (settings)
//   6. Reconcile lazy-skills ConfigMap (create when lazy skills exist; delete when none)
//   7. Ensure ServiceAccount
//   8. Ensure Deployment (mounts /etc/kape/skills when lazy skills present)
//   9. Ensure KEDA ScaledObject
//  10. Sync labels (schema-ref + tool-ref-{name} for unioned tools + skill-ref-{name})
//  11. Refresh handler after label patch
//  12. Read Deployment status → build conditions
//  13. Patch status
func (r *HandlerReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("handler", key)

	// 1. Fetch
	handler, err := r.handlers.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeHandler: %w", err)
	}
	if handler == nil {
		return ctrl.Result{}, nil
	}

	// 2. Dependency gate
	deps, depsReady, gateMsg, gateReason, err := r.validateDependencies(ctx, handler)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !depsReady {
		handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
			Type:    "DependenciesReady",
			Status:  metav1.ConditionFalse,
			Reason:  gateReason,
			Message: gateMsg,
		})
		// Ready rollup is computed at the end (Step 12); for the early-exit
		// case we set it explicitly here to keep the fast-path simple.
		handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
			Type:   "Ready",
			Status: metav1.ConditionFalse,
			Reason: "DependenciesNotReady",
		})
		_ = r.handlers.UpdateStatus(ctx, handler)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
		Type:   "DependenciesReady",
		Status: metav1.ConditionTrue,
		Reason: ReasonReady,
	})

	// 3. Validate scaling
	if handler.Spec.Scaling != nil && handler.Spec.Scaling.ScaleToZero && handler.Spec.Scaling.MinReplicas >= 1 {
		handler.Status.Conditions = setCondition(handler.Status.Conditions, metav1.Condition{
			Type:    "ScalingConfigured",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidScalingConfig",
			Message: "scaleToZero: true requires minReplicas: 0",
		})
		_ = r.handlers.UpdateStatus(ctx, handler)
		return ctrl.Result{}, nil // terminal
	}

	// 4. Compute hashes
	rolloutHash, err := computeRolloutHash(handler, deps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("computing rollout hash: %w", err)
	}
	consumerName := strings.ReplaceAll(handler.Spec.Trigger.Type, ".", "-")

	// 5. Load config and render settings.toml
	cfg, err := r.kapeConfig.Load(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("loading kape-config: %w", err)
	}
	eagerSkills := deps.EagerSkills()
	lazySkills := deps.LazySkills()
	tomlContent, err := r.tomlRenderer.Render(handler, deps.Schema, deps.Tools, eagerSkills, lazySkills, cfg)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("rendering settings.toml: %w", err)
	}
	if err := r.configMaps.Ensure(ctx, handler, tomlContent); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ConfigMap: %w", err)
	}
	log.V(1).Info("ConfigMap reconciled")

	// 6. Reconcile lazy-skills ConfigMap (create when lazy skills exist, delete otherwise)
	lazySkillsPresent := len(lazySkills) > 0
	if lazySkillsPresent {
		if err := r.skillConfigMaps.Ensure(ctx, handler, lazySkills); err != nil {
			return ctrl.Result{}, fmt.Errorf("ensuring kape-skills ConfigMap: %w", err)
		}
	} else {
		if err := r.skillConfigMaps.Delete(ctx, handler); err != nil {
			return ctrl.Result{}, fmt.Errorf("deleting kape-skills ConfigMap: %w", err)
		}
	}

	// 7. Ensure ServiceAccount
	if err := r.serviceAccounts.Ensure(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ServiceAccount: %w", err)
	}

	// 8. Ensure Deployment (mounts /etc/kape/skills when lazy skills present)
	if err := r.deployments.Ensure(ctx, handler, cfg, rolloutHash, deps.Tools, lazySkillsPresent); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring Deployment: %w", err)
	}
	log.V(1).Info("Deployment reconciled", "rolloutHash", rolloutHash)

	// 9. Ensure KEDA ScaledObject
	soKey := types.NamespacedName{Name: handlerScaledObjectName(handler), Namespace: handler.Namespace}
	existingConsumer, soFound, err := r.scaledObjects.GetConsumerName(ctx, soKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading ScaledObject: %w", err)
	}
	if soFound && existingConsumer != consumerName {
		// trigger.type changed — delete and recreate
		if err := r.scaledObjects.Delete(ctx, soKey); err != nil {
			return ctrl.Result{}, fmt.Errorf("deleting stale ScaledObject: %w", err)
		}
	}
	if err := r.scaledObjects.Ensure(ctx, handler, consumerName, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("ensuring ScaledObject: %w", err)
	}

	// 10. Sync labels
	//
	// Per spec D7+D8:
	//   - kape.io/schema-ref={name}
	//   - kape.io/tool-ref-{name}=true for EVERY tool in deps.Tools
	//     (handler-direct + skill-pulled — transitive)
	//   - kape.io/skill-ref-{name}=true for every entry in handler.spec.skills[]
	//
	// Slice 4 reads kape.io/skill-ref-* to enqueue handler reconciles when a
	// referenced KapeSkill changes. This slice produces the labels only —
	// no controller wiring is added here.
	labels := map[string]string{"kape.io/schema-ref": handler.Spec.SchemaRef}
	for _, t := range deps.Tools {
		labels["kape.io/tool-ref-"+t.Name] = "true"
	}
	for _, s := range handler.Spec.Skills {
		labels["kape.io/skill-ref-"+s.Ref] = "true"
	}
	if err := r.handlers.SyncLabels(ctx, handler, labels); err != nil {
		log.Error(err, "failed to sync labels")
	}

	// 11. Refresh handler after label patch
	handler, err = r.handlers.Get(ctx, key)
	if err != nil || handler == nil {
		return ctrl.Result{}, err
	}

	// 12. Read Deployment status → build conditions
	depKey := types.NamespacedName{Name: handlerDeploymentName(handler), Namespace: handler.Namespace}
	depStatus, depFound, err := r.deployments.GetStatus(ctx, depKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading Deployment status: %w", err)
	}
	handler.Status.Conditions = buildHandlerConditions(depStatus, depFound, handler.Status.Conditions)
	if depFound && depStatus != nil {
		handler.Status.Replicas = depStatus.ReadyReplicas
	}

	// 13. Patch status
	if err := r.handlers.UpdateStatus(ctx, handler); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}
```

- [ ] **Step 2: Replace `buildHandlerConditions` with the forward-compatible `Ready` rollup**

Replace `buildHandlerConditions` (lines 264-289) with:

```go
// buildHandlerConditions computes DeploymentAvailable from the Deployment
// status and then folds the entire condition set into the Ready rollup.
//
// Per spec §2.3 the rollup is the NEGATIVE form: Ready=True iff no condition
// in the slice is explicitly False. This is forward-compatible with future
// owners (slice 6's KapeProxyReady, etc.) without changes here.
func buildHandlerConditions(depStatus *appsv1.DeploymentStatus, depFound bool, existing []metav1.Condition) []metav1.Condition {
	deploymentAvailable := metav1.Condition{Type: "DeploymentAvailable"}

	switch {
	case !depFound:
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "DeploymentNotFound"
	case depStatus == nil || depStatus.ReadyReplicas == 0:
		deploymentAvailable.Status = metav1.ConditionFalse
		deploymentAvailable.Reason = "MinimumReplicasUnavailable"
	default:
		deploymentAvailable.Status = metav1.ConditionTrue
		deploymentAvailable.Reason = "Available"
		deploymentAvailable.Message = fmt.Sprintf("%d/%d replicas ready", depStatus.ReadyReplicas, depStatus.Replicas)
	}
	existing = setCondition(existing, deploymentAvailable)
	existing = setCondition(existing, computeReadyRollup(existing))
	return existing
}

// computeReadyRollup folds every condition in the slice (except "Ready"
// itself) into the Ready rollup using the negative form: Ready=True iff no
// condition is explicitly False. Per spec §2.3 forward-compat rule.
//
// Reason precedence on False: first False condition wins (deterministic via
// slice order). Reason on True is "Ready".
func computeReadyRollup(conditions []metav1.Condition) metav1.Condition {
	for _, c := range conditions {
		if c.Type == "Ready" {
			continue
		}
		if c.Status == metav1.ConditionFalse {
			return metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  c.Reason,
				Message: c.Message,
			}
		}
	}
	return metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: ReasonReady}
}
```

- [ ] **Step 3: Verify the file (and the whole operator module) compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./...
```

Expected: only the wiring sites in `operator/controller/handler.go` and `operator/cmd/main.go` may still fail — fix them in Tasks 15 and 16. If there are errors in `controller/reconcile/handler.go` itself, fix them now.

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/controller/reconcile/handler.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(reconcile): handler skill gate + tool union + lazy ConfigMap step + Ready rollup"
```

---

## Task 15: Update `controller/handler.go` to plumb new dependencies

**Files:**
- Modify: `operator/controller/handler.go`

- [ ] **Step 1: Inspect the current file**

```bash
sed -n '1,80p' /home/tony/projects/kape-io/operator/controller/handler.go
```

The file wires `NewHandlerReconciler(...)` from a `Setup*` function. You need to add `skills` (`ports.SkillRepository`) and `skillConfigMaps` (`ports.SkillConfigMapPort`) parameters and pass them through.

- [ ] **Step 2: Edit the signature**

Update whichever function constructs `HandlerReconciler` (typically `SetupHandlerReconciler` or `NewHandlerController`) to accept the two new parameters and forward them into `reconcile.NewHandlerReconciler`. The exact location depends on the existing layout — apply the minimal edit to keep the build green.

Example shape:

```go
func SetupHandlerReconciler(
	mgr ctrl.Manager,
	handlers ports.HandlerRepository,
	schemas ports.SchemaRepository,
	tools ports.ToolRepository,
	skills ports.SkillRepository,         // NEW
	configMaps ports.ConfigMapPort,
	skillConfigMaps ports.SkillConfigMapPort, // NEW
	serviceAccounts ports.ServiceAccountPort,
	deployments ports.DeploymentPort,
	scaledObjects ports.ScaledObjectPort,
	tomlRenderer ports.TOMLRenderer,
	kapeConfig ports.KapeConfigLoader,
	maxConcurrent int,
) error {
	rec := reconcile.NewHandlerReconciler(
		handlers, schemas, tools, skills,
		configMaps, skillConfigMaps,
		serviceAccounts, deployments, scaledObjects,
		tomlRenderer, kapeConfig,
	)
	// ... existing controller-runtime SetupWithManager wiring unchanged
}
```

- [ ] **Step 3: Verify the file compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./controller/...
```

Expected: no errors after this step.

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/controller/handler.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(controller): plumb SkillRepository + SkillConfigMapPort into handler setup"
```

---

## Task 16: Update `cmd/main.go` to instantiate new adapters

**Files:**
- Modify: `operator/cmd/main.go`

- [ ] **Step 1: Inspect main.go's adapter wiring**

```bash
grep -n "NewHandlerReconciler\|NewSkillRepository\|NewConfigMapAdapter" /home/tony/projects/kape-io/operator/cmd/main.go
```

You should find one call to whichever `Setup*` you modified in Task 15 plus existing adapter constructors.

- [ ] **Step 2: Add `SkillRepository` + `SkillConfigMapAdapter` instantiation**

Near the existing adapter setup (alongside `k8sadapters.NewSchemaRepository(c)`, etc.), add:

```go
skillRepo := k8sadapters.NewSkillRepository(mgr.GetClient())
skillConfigMaps := k8sadapters.NewSkillConfigMapAdapter(mgr.GetClient())
```

Then pass them into the `Setup*` call you updated in Task 15. Match the exact parameter order from Task 15 Step 2.

- [ ] **Step 3: Verify the binary builds**

```bash
cd /home/tony/projects/kape-io/operator && go build ./cmd/...
```

Expected: no errors.

- [ ] **Step 4: Run the full operator test suite (existing tests should still pass — handler tests will be updated in Task 19)**

```bash
cd /home/tony/projects/kape-io/operator && go test ./...
```

Expected: existing handler tests in `controller/reconcile/handler_test.go` will FAIL because they call the old `NewHandlerReconciler` signature. Other packages should still pass. Task 19 fixes the handler tests; we are pinning down `main.go` first to keep the binary compilable.

- [ ] **Step 5: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/cmd/main.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "feat(cmd): wire SkillRepository + SkillConfigMapAdapter in main.go"
```

---

## Task 17: Write `system_prompt_test.go`

**Files:**
- Create: `operator/controller/reconcile/system_prompt_test.go`

- [ ] **Step 1: Write the test file**

```go
package reconcile_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kape-io/kape/operator/controller/reconcile"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

func mkSkill(name, description, instruction string, lazy bool) v1alpha1.KapeSkill {
	return v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kape-system"},
		Spec: v1alpha1.KapeSkillSpec{
			Description: description,
			Instruction: instruction,
			LazyLoad:    lazy,
		},
	}
}

func handlerWithPrompt(prompt string) *v1alpha1.KapeHandler {
	return &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system"},
		Spec: v1alpha1.KapeHandlerSpec{
			LLM: v1alpha1.LLMSpec{Provider: "p", Model: "m", SystemPrompt: prompt},
		},
	}
}

func TestAssembleSystemPrompt_HandlerOnly(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(handlerWithPrompt("base prompt"), nil, nil)
	assert.Equal(t, "base prompt", got)
}

func TestAssembleSystemPrompt_HandlerPlusEager_Single(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(
		handlerWithPrompt("base"),
		[]v1alpha1.KapeSkill{mkSkill("s1", "d1", "INSTR-1", false)},
		nil,
	)
	assert.Equal(t, "base\n\n---\n\nINSTR-1", got)
}

func TestAssembleSystemPrompt_HandlerPlusEager_MultipleInDeclarationOrder(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(
		handlerWithPrompt("base"),
		[]v1alpha1.KapeSkill{
			mkSkill("s1", "d1", "FIRST", false),
			mkSkill("s2", "d2", "SECOND", false),
		},
		nil,
	)
	expected := "base\n\n---\n\nFIRST\n\n---\n\nSECOND"
	assert.Equal(t, expected, got)
}

func TestAssembleSystemPrompt_HandlerPlusLazyOnly(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(
		handlerWithPrompt("base"),
		nil,
		[]v1alpha1.KapeSkill{
			mkSkill("check-orders", "Investigates order events", "instr-ignored", true),
			mkSkill("check-shifts", "Looks at shift handovers", "instr-ignored", true),
		},
	)
	// Two newlines (not "---") between handler prompt and lazy preamble when
	// no eager skills exist.
	assert.True(t, strings.HasPrefix(got, "base\n\n"))
	assert.False(t, strings.Contains(got, "base\n\n---\n\nAvailable skills"),
		"no separator should be emitted when eager skills are absent")
	assert.Contains(t, got, "Available skills (call load_skill with the skill name to retrieve full instructions):")
	assert.Contains(t, got, "- check-orders: Investigates order events")
	assert.Contains(t, got, "- check-shifts: Looks at shift handovers")
	assert.Contains(t, got, "When you determine a skill is relevant, call load_skill with its name before proceeding.")
	// Lazy instructions must NOT appear in the prompt; only descriptions.
	assert.False(t, strings.Contains(got, "instr-ignored"))
}

func TestAssembleSystemPrompt_HandlerPlusEagerAndLazy_OrderAndSeparators(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(
		handlerWithPrompt("base"),
		[]v1alpha1.KapeSkill{mkSkill("eager-1", "d", "EAGER-INSTR", false)},
		[]v1alpha1.KapeSkill{mkSkill("lazy-1", "lazy desc", "lazy-instr-ignored", true)},
	)
	// Expected layout:
	//   base
	//   ---
	//   EAGER-INSTR
	//   ---
	//   Available skills ...
	expectedPrefix := "base\n\n---\n\nEAGER-INSTR\n\n---\n\nAvailable skills"
	assert.True(t, strings.HasPrefix(got, expectedPrefix), "actual prefix:\n%q", got)
	assert.Contains(t, got, "- lazy-1: lazy desc")
	assert.False(t, strings.Contains(got, "lazy-instr-ignored"))
}

func TestAssembleSystemPrompt_DeterministicForSameInputs(t *testing.T) {
	h := handlerWithPrompt("base")
	eager := []v1alpha1.KapeSkill{mkSkill("e1", "d", "E1", false)}
	lazy := []v1alpha1.KapeSkill{mkSkill("l1", "ld", "li", true)}
	a := reconcile.AssembleSystemPrompt(h, eager, lazy)
	b := reconcile.AssembleSystemPrompt(h, eager, lazy)
	assert.Equal(t, a, b)
}

func TestAssembleSystemPrompt_EmptyHandlerPromptIsAllowed(t *testing.T) {
	got := reconcile.AssembleSystemPrompt(
		handlerWithPrompt(""),
		[]v1alpha1.KapeSkill{mkSkill("e1", "d", "E1-INSTR", false)},
		nil,
	)
	// Leading separator is fine — kubebuilder marker requires non-empty
	// systemPrompt at admission time; this test pins behaviour for unit
	// callers that bypass admission.
	assert.Equal(t, "\n\n---\n\nE1-INSTR", got)
}
```

- [ ] **Step 2: Run the tests**

```bash
cd /home/tony/projects/kape-io/operator && go test ./controller/reconcile/ -run TestAssembleSystemPrompt -v
```

Expected: all 7 tests PASS.

- [ ] **Step 3: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/controller/reconcile/system_prompt_test.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "test(reconcile): system_prompt assembly unit tests"
```

---

## Task 18: Extend `renderer_test.go` for skill assembly + tool stability

**Files:**
- Modify: `operator/infra/toml/renderer_test.go`

- [ ] **Step 1: Inspect existing test layout**

```bash
sed -n '1,40p' /home/tony/projects/kape-io/operator/infra/toml/renderer_test.go
```

You'll see a pattern for building a handler/schema/tools fixture and asserting against rendered TOML. Add the new tests in the same style.

- [ ] **Step 2: Add tests at the bottom of the file**

```go
func TestRender_SystemPromptIncludesEagerSkillInDeclarationOrder(t *testing.T) {
	r := tomlrenderer.NewRenderer()
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system"},
		Spec: v1alpha1.KapeHandlerSpec{
			Trigger:   v1alpha1.TriggerSpec{Source: "alertmanager", Type: "kape.events.test"},
			LLM:       v1alpha1.LLMSpec{Provider: "anthropic", Model: "claude-3", SystemPrompt: "base"},
			SchemaRef: "my-schema",
		},
	}
	schema := &v1alpha1.KapeSchema{Spec: v1alpha1.KapeSchemaSpec{
		Version: "v1",
		JSONSchema: v1alpha1.JSONSchemaObject{Type: "object"},
	}}
	eager := []v1alpha1.KapeSkill{
		{ObjectMeta: metav1.ObjectMeta{Name: "first"}, Spec: v1alpha1.KapeSkillSpec{Instruction: "FIRST-INSTR"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "second"}, Spec: v1alpha1.KapeSkillSpec{Instruction: "SECOND-INSTR"}},
	}
	out, err := r.Render(handler, schema, nil, eager, nil, domainconfig.KapeConfig{})
	require.NoError(t, err)
	// FIRST appears before SECOND
	idx1 := strings.Index(out, "FIRST-INSTR")
	idx2 := strings.Index(out, "SECOND-INSTR")
	require.NotEqual(t, -1, idx1, "first skill text missing:\n%s", out)
	require.NotEqual(t, -1, idx2, "second skill text missing:\n%s", out)
	assert.Less(t, idx1, idx2, "skills must appear in declaration order")
	// Both inside system_prompt — assert by checking the [llm] block
	assert.Contains(t, out, "system_prompt = ")
	assert.Contains(t, out, "FIRST-INSTR\n\n---\n\nSECOND-INSTR")
}

func TestRender_SystemPromptIncludesLazyPreamble(t *testing.T) {
	r := tomlrenderer.NewRenderer()
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system"},
		Spec: v1alpha1.KapeHandlerSpec{
			Trigger:   v1alpha1.TriggerSpec{Source: "alertmanager", Type: "kape.events.test"},
			LLM:       v1alpha1.LLMSpec{Provider: "anthropic", Model: "claude-3", SystemPrompt: "base"},
			SchemaRef: "my-schema",
		},
	}
	schema := &v1alpha1.KapeSchema{Spec: v1alpha1.KapeSchemaSpec{Version: "v1", JSONSchema: v1alpha1.JSONSchemaObject{Type: "object"}}}
	lazy := []v1alpha1.KapeSkill{
		{ObjectMeta: metav1.ObjectMeta{Name: "check-orders"}, Spec: v1alpha1.KapeSkillSpec{Description: "investigates orders", Instruction: "should-not-appear", LazyLoad: true}},
	}
	out, err := r.Render(handler, schema, nil, nil, lazy, domainconfig.KapeConfig{})
	require.NoError(t, err)
	assert.Contains(t, out, "Available skills (call load_skill with the skill name to retrieve full instructions):")
	assert.Contains(t, out, "- check-orders: investigates orders")
	assert.NotContains(t, out, "should-not-appear", "lazy skill instruction must not be inlined")
}

func TestRender_NoSkills_PromptUnchanged(t *testing.T) {
	r := tomlrenderer.NewRenderer()
	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "h", Namespace: "kape-system"},
		Spec: v1alpha1.KapeHandlerSpec{
			Trigger:   v1alpha1.TriggerSpec{Source: "alertmanager", Type: "kape.events.test"},
			LLM:       v1alpha1.LLMSpec{Provider: "anthropic", Model: "claude-3", SystemPrompt: "base prompt only"},
			SchemaRef: "my-schema",
		},
	}
	schema := &v1alpha1.KapeSchema{Spec: v1alpha1.KapeSchemaSpec{Version: "v1", JSONSchema: v1alpha1.JSONSchemaObject{Type: "object"}}}
	out, err := r.Render(handler, schema, nil, nil, nil, domainconfig.KapeConfig{})
	require.NoError(t, err)
	// Prompt should not contain the lazy preamble nor any "---" separators
	// originating from skill assembly
	assert.NotContains(t, out, "Available skills (call load_skill")
	assert.NotContains(t, out, "\n\n---\n\n")
	assert.Contains(t, out, "base prompt only")
}
```

Add the import for `"strings"` if not already present.

- [ ] **Step 3: Run the renderer tests**

```bash
cd /home/tony/projects/kape-io/operator && go test ./infra/toml/... -v
```

Expected: all existing renderer tests + 3 new tests PASS.

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/infra/toml/renderer_test.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "test(toml): renderer assembles skill prompt segments correctly"
```

---

## Task 19: Update `handler_test.go` for new constructor + skill scenarios

**Files:**
- Modify: `operator/controller/reconcile/handler_test.go`

- [ ] **Step 1: Update existing test setup helper**

The 4 existing tests build a `HandlerReconciler` via the old constructor signature. Add a helper at the top of the file (after `baseKapeHandler`) and update existing call sites:

```go
func newReconciler(c client.Client) *reconcile.HandlerReconciler {
	return reconcile.NewHandlerReconciler(
		k8sadapters.NewHandlerRepository(c),
		k8sadapters.NewSchemaRepository(c),
		k8sadapters.NewToolRepository(c),
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewConfigMapAdapter(c),
		k8sadapters.NewSkillConfigMapAdapter(c),
		k8sadapters.NewServiceAccountAdapter(c),
		k8sadapters.NewDeploymentAdapter(c),
		k8sadapters.NewScaledObjectAdapter(c),
		tomlrenderer.NewRenderer(),
		&fakeConfigLoader{},
	)
}
```

Add the new import: `"sigs.k8s.io/controller-runtime/pkg/client"`.

Replace each existing `r := reconcile.NewHandlerReconciler(...)` block in the four existing tests with `r := newReconciler(c)`.

Also extend `newHandlerScheme` to register `KapeSkill` so the fake client recognises it (the call to `v1alpha1.AddToScheme(s)` already covers it via `kapeskill_types.go`'s `init()` — verify by running tests; no change needed if `AddToScheme` is global).

- [ ] **Step 2: Add a `readySkill` fixture helper**

After `readyTool`, add:

```go
func readySkill(name, ns string, lazy bool, toolRefs []string) *v1alpha1.KapeSkill {
	refs := make([]v1alpha1.SkillToolRef, len(toolRefs))
	for i, r := range toolRefs {
		refs[i] = v1alpha1.SkillToolRef{Ref: r}
	}
	return &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1alpha1.KapeSkillSpec{
			Description: "desc " + name,
			Instruction: "INSTR-" + strings.ToUpper(name),
			LazyLoad:    lazy,
			Tools:       refs,
		},
		Status: v1alpha1.KapeSkillStatus{
			Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready"}},
		},
	}
}
```

(Update the helper's import block to include `"strings"`.)

Also update `baseKapeHandler` to accept skills:

```go
func baseKapeHandler(name, ns, schemaRef string, toolRefs []string, skillRefs ...string) *v1alpha1.KapeHandler {
	tools := make([]v1alpha1.ToolRef, len(toolRefs))
	for i, r := range toolRefs {
		tools[i] = v1alpha1.ToolRef{Ref: r}
	}
	skills := make([]v1alpha1.SkillRef, len(skillRefs))
	for i, r := range skillRefs {
		skills[i] = v1alpha1.SkillRef{Ref: r}
	}
	return &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: "uid-h"},
		Spec: v1alpha1.KapeHandlerSpec{
			Trigger:   v1alpha1.TriggerSpec{Source: "alertmanager", Type: "kape.events.test"},
			LLM:       v1alpha1.LLMSpec{Provider: "anthropic", Model: "claude-3", SystemPrompt: "test"},
			SchemaRef: schemaRef,
			Tools:     tools,
			Skills:    skills,
			Actions:   []v1alpha1.ActionSpec{},
		},
	}
}
```

The four existing tests pass no skill refs, so the `...` variadic keeps them compiling unchanged.

- [ ] **Step 3: Add the slice 3 test cases**

Append the following tests to `handler_test.go`:

```go
func TestHandlerReconciler_SkillNotFound_RequeuePending(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "missing-skill")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema).
		WithStatusSubresource(handler, schema).
		Build()
	r := newReconciler(c)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	assert.Equal(t, int64(30), int64(result.RequeueAfter.Seconds()))

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	dep := findCondition(got.Status.Conditions, "DependenciesReady")
	require.NotNil(t, dep)
	assert.Equal(t, metav1.ConditionFalse, dep.Status)
	assert.Equal(t, "KapeSkillNotFound", dep.Reason)
}

func TestHandlerReconciler_SkillNotReady_RequeuePending(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	skill := readySkill("not-ready-skill", "kape-system", false, nil)
	skill.Status.Conditions[0].Status = metav1.ConditionFalse
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "not-ready-skill")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, skill).
		WithStatusSubresource(handler, schema, skill).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	dep := findCondition(got.Status.Conditions, "DependenciesReady")
	require.NotNil(t, dep)
	assert.Equal(t, metav1.ConditionFalse, dep.Status)
	assert.Equal(t, "KapeSkillNotReady", dep.Reason)
}

func TestHandlerReconciler_LazySkill_CreatesKapeSkillsConfigMapAndMount(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("order-mcp", "kape-system", "mcp")
	skill := readySkill("check-orders", "kape-system", true, []string{"order-mcp"})
	skill.Spec.Description = "investigates orders"
	skill.Spec.Instruction = "do-the-investigation"
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "check-orders")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool, skill).
		WithStatusSubresource(handler, schema, tool, skill).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)

	// kape-skills-h ConfigMap exists with one file
	var cm corev1.ConfigMap
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-skills-h", Namespace: "kape-system"}, &cm)
	require.NoError(t, err)
	assert.Equal(t, "do-the-investigation", cm.Data["check-orders.txt"])

	// Deployment mounts /etc/kape/skills
	var dep appsv1.Deployment
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep)
	require.NoError(t, err)
	mounts := dep.Spec.Template.Spec.Containers[0].VolumeMounts
	var foundSkillsMount bool
	for _, m := range mounts {
		if m.Name == "kape-skills" && m.MountPath == "/etc/kape/skills" {
			foundSkillsMount = true
		}
	}
	assert.True(t, foundSkillsMount, "handler container must mount /etc/kape/skills when lazy skills present; mounts=%+v", mounts)
}

func TestHandlerReconciler_OnlyEagerSkills_NoKapeSkillsConfigMap(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("order-mcp", "kape-system", "mcp")
	skill := readySkill("inline-skill", "kape-system", false, []string{"order-mcp"})
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "inline-skill")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool, skill).
		WithStatusSubresource(handler, schema, tool, skill).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)

	// No kape-skills-h ConfigMap
	var cm corev1.ConfigMap
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-skills-h", Namespace: "kape-system"}, &cm)
	assert.True(t, apierrors.IsNotFound(err), "kape-skills-h must NOT exist when only eager skills present, got err=%v", err)

	// Deployment does NOT mount /etc/kape/skills
	var dep appsv1.Deployment
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep)
	require.NoError(t, err)
	for _, m := range dep.Spec.Template.Spec.Containers[0].VolumeMounts {
		assert.NotEqual(t, "kape-skills", m.Name, "no kape-skills mount when only eager skills present")
	}
}

func TestHandlerReconciler_SkillRemoved_DeletesKapeSkillsConfigMap(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("order-mcp", "kape-system", "mcp")
	skill := readySkill("check-orders", "kape-system", true, []string{"order-mcp"})
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "check-orders")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool, skill).
		WithStatusSubresource(handler, schema, tool, skill).
		Build()
	r := newReconciler(c)

	// First reconcile: ConfigMap created
	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	var cm corev1.ConfigMap
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-skills-h", Namespace: "kape-system"}, &cm))

	// Mutate handler to remove the skill ref
	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	got.Spec.Skills = nil
	require.NoError(t, c.Update(context.Background(), got))

	// Second reconcile: ConfigMap deleted
	_, err = r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	err = c.Get(context.Background(), types.NamespacedName{Name: "kape-skills-h", Namespace: "kape-system"}, &cm)
	assert.True(t, apierrors.IsNotFound(err), "kape-skills-h must be deleted after skill ref removed; got err=%v", err)
}

func TestHandlerReconciler_LabelSync_TransitiveToolAndSkillLabels(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	directTool := readyTool("k8s-mcp", "kape-system", "mcp")
	skillTool := readyTool("order-mcp", "kape-system", "mcp")
	skill := readySkill("check-orders", "kape-system", false, []string{"order-mcp"})
	handler := baseKapeHandler("h", "kape-system", "my-schema", []string{"k8s-mcp"}, "check-orders")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, directTool, skillTool, skill).
		WithStatusSubresource(handler, schema, directTool, skillTool, skill).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Equal(t, "true", got.Labels["kape.io/tool-ref-k8s-mcp"], "direct tool label")
	assert.Equal(t, "true", got.Labels["kape.io/tool-ref-order-mcp"], "transitive (skill-pulled) tool label per D8")
	assert.Equal(t, "true", got.Labels["kape.io/skill-ref-check-orders"], "skill ref label per D7")
}

func TestComputeRolloutHash_ChangesWhenSkillSpecChanges(t *testing.T) {
	// Direct unit test on the (unexported) hash function via two reconciles.
	// Simulate a content change in a referenced KapeSkill and assert the
	// rollout-hash annotation on the Deployment differs across the two runs.
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("order-mcp", "kape-system", "mcp")
	skill := readySkill("check-orders", "kape-system", false, []string{"order-mcp"})
	skill.Spec.Instruction = "VERSION-1"
	handler := baseKapeHandler("h", "kape-system", "my-schema", nil, "check-orders")

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler, schema, tool, skill).
		WithStatusSubresource(handler, schema, tool, skill).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	var dep1 appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep1))
	hash1 := dep1.Annotations["kape.io/rollout-hash"]
	require.NotEmpty(t, hash1)

	// Mutate skill instruction
	gotSkill := &v1alpha1.KapeSkill{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "check-orders", Namespace: "kape-system"}, gotSkill))
	gotSkill.Spec.Instruction = "VERSION-2"
	require.NoError(t, c.Update(context.Background(), gotSkill))

	_, err = r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	var dep2 appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &dep2))
	hash2 := dep2.Annotations["kape.io/rollout-hash"]

	assert.NotEqual(t, hash1, hash2, "rollout hash must change when a referenced skill's spec changes")
}

func TestComputeRolloutHash_ChangesWhenSkillOrderChanges(t *testing.T) {
	s := newHandlerScheme()
	schema := readySchema("my-schema", "kape-system")
	tool := readyTool("order-mcp", "kape-system", "mcp")
	skillA := readySkill("a-skill", "kape-system", false, []string{"order-mcp"})
	skillB := readySkill("b-skill", "kape-system", false, []string{"order-mcp"})

	// First reconcile: order = [a, b]
	handler1 := baseKapeHandler("h", "kape-system", "my-schema", nil, "a-skill", "b-skill")
	c1 := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler1, schema, tool, skillA, skillB).
		WithStatusSubresource(handler1, schema, tool, skillA, skillB).
		Build()
	r1 := newReconciler(c1)
	_, err := r1.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	var d1 appsv1.Deployment
	require.NoError(t, c1.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &d1))
	hashAB := d1.Annotations["kape.io/rollout-hash"]

	// Second fresh reconcile: order = [b, a]
	handler2 := baseKapeHandler("h", "kape-system", "my-schema", nil, "b-skill", "a-skill")
	c2 := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler2, schema, tool, skillA, skillB).
		WithStatusSubresource(handler2, schema, tool, skillA, skillB).
		Build()
	r2 := newReconciler(c2)
	_, err = r2.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)
	var d2 appsv1.Deployment
	require.NoError(t, c2.Get(context.Background(), types.NamespacedName{Name: "kape-handler-h", Namespace: "kape-system"}, &d2))
	hashBA := d2.Annotations["kape.io/rollout-hash"]

	assert.NotEqual(t, hashAB, hashBA, "rollout hash must change when skill declaration order changes (D13)")
}

func TestHandlerReconciler_ReadyRollup_FalseWhenAnyConditionFalse(t *testing.T) {
	// Force DependenciesReady=False via a missing schema and assert Ready=False.
	s := newHandlerScheme()
	handler := baseKapeHandler("h", "kape-system", "missing-schema", nil)

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(handler).
		WithStatusSubresource(handler).
		Build()
	r := newReconciler(c)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	require.NoError(t, err)

	got, _ := k8sadapters.NewHandlerRepository(c).Get(context.Background(), types.NamespacedName{Name: "h", Namespace: "kape-system"})
	ready := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionFalse, ready.Status)
}
```

- [ ] **Step 4: Run the handler tests**

```bash
cd /home/tony/projects/kape-io/operator && go test ./controller/reconcile/... -run TestHandlerReconciler -v
```

Expected: existing 4 tests + 8 new slice 3 tests PASS. The hash-stability tests run two full reconciles — total runtime should still be under 30s on the fake client.

- [ ] **Step 5: Run the full operator test suite**

```bash
cd /home/tony/projects/kape-io/operator && go test ./...
```

Expected: ALL tests pass. No `FAIL` output.

- [ ] **Step 6: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add operator/controller/reconcile/handler_test.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "test(reconcile): handler skill gate, lazy ConfigMap, label sync, hash stability"
```

---

## Task 20: Snyk Code scan + fix any issues

> Use the `mcp__Snyk__snyk_code_scan` MCP tool (not the `snyk` CLI).

- [ ] **Step 1: Run Snyk Code scan on operator/**

Call `mcp__Snyk__snyk_code_scan` with path `operator/` from the worktree root:

```
tool: mcp__Snyk__snyk_code_scan
input: {
  "path": "/home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl/operator"
}
```

Expected: `0` findings on slice 3 files. If any High/Critical findings appear in files this slice modified, fix them inline and re-run the scan until clean. Do not silence by adding ignores.

- [ ] **Step 2: If findings exist, fix them and rescan**

Apply minimal fixes per Snyk's suggested remediation. Re-run the scan:

```
tool: mcp__Snyk__snyk_code_scan
input: {
  "path": "/home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl/operator"
}
```

- [ ] **Step 3: Commit any fixes (only if Step 2 made changes)**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl add -A operator/
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl commit -m "fix(security): address Snyk Code findings in slice 3 files"
```

---

## Task 21: SBOM scans (per kape-io CLAUDE.md PR checklist)

> Use `mcp__Snyk__snyk_sbom_scan` with format `cyclonedx1.4+json`. Record the component count + flagged-package count for each module — they go in the PR comment in Task 22.

- [ ] **Step 1: Run SBOM scan on adapters**

```
tool: mcp__Snyk__snyk_sbom_scan
input: {
  "path": "/home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl/adapters",
  "format": "cyclonedx1.4+json"
}
```

Record: component count, any flagged packages.

- [ ] **Step 2: Run SBOM scan on operator**

```
tool: mcp__Snyk__snyk_sbom_scan
input: {
  "path": "/home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl/operator",
  "format": "cyclonedx1.4+json"
}
```

Record: component count, any flagged packages.

- [ ] **Step 3: Run SBOM scan on task-service**

```
tool: mcp__Snyk__snyk_sbom_scan
input: {
  "path": "/home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl/task-service",
  "format": "cyclonedx1.4+json"
}
```

Record: component count, any flagged packages.

---

## Task 22: Push branch, open PR, post SBOM comment

- [ ] **Step 1: Push the branch**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice3-impl push -u origin feat/phase6-slice3-impl
```

- [ ] **Step 2: Open the PR**

```bash
gh pr create \
  --repo dzungtr/kape \
  --base main \
  --head feat/phase6-slice3-impl \
  --title "feat(phase6): handler skill gate + tool union + system prompt + lazy ConfigMap (slice 3)" \
  --body "$(cat <<'EOF'
## Summary

- Adds `Skills []SkillRef` to `KapeHandlerSpec`; regenerates DeepCopy + CRD
- Introduces `resolvedDependencies` carrier (per IMPLEMENTATION-SPEC §2.1) used by hash, deployment, prompt assembly, label sync
- Extends `validateDependencies` to resolve skills + skill-pulled tools, dedup by `KapeTool.Name`, sort for hash stability (D13)
- Adds `KapeSkillNotFound` and `KapeSkillNotReady` reasons for `DependenciesReady` (per §2.3)
- Extends `computeRolloutHash` with sorted tools + ordered skill Specs (skills NOT sorted — D13 declaration order)
- Adds pure `AssembleSystemPrompt(handler, eager, lazy) string` and wires it into `toml.Renderer.Render`
- Reconciles `kape-skills-{handler-name}` ConfigMap when lazy skills exist; deletes it when none
- Mounts `/etc/kape/skills` from that ConfigMap on the handler container when lazy skills present
- Extends label sync: `kape.io/tool-ref-{name}=true` for every unioned tool (D8 transitive); `kape.io/skill-ref-{name}=true` per skill ref (D7)
- Flips `Ready` rollup to forward-compatible negative form: `Ready=True` iff no condition is explicitly `False` (§2.3 — supports slice 6's `KapeProxyReady` without further edits)

## Acceptance criteria (from Phase 6 README + IMPLEMENTATION-SPEC §1 slice 3)

- [x] Apply KapeHandler + KapeSkill (lazyLoad: true) → `kape-skills-{name}` ConfigMap exists, mounted at `/etc/kape/skills/`
- [x] KapeSkill referencing a not-Ready KapeTool puts the KapeHandler in Pending with reason `KapeSkillNotReady`
- [x] Eager-skill instruction text appears in settings.toml in declaration order
- [x] `computeRolloutHash` changes when a referenced KapeSkill's `.Spec` content changes

All four demonstrated by tests in:
- `operator/controller/reconcile/handler_test.go` (envtest-style)
- `operator/controller/reconcile/system_prompt_test.go` (unit)
- `operator/infra/toml/renderer_test.go` (unit)

## Out of scope (per spec)

- Cross-resource watch wiring for KapeSkill — slice 4
- kapeproxy-config rendering / sidecar reshape — slice 5
- `KapeProxyReady` / `KapeProxyDegraded` conditions — slice 6
- Handler-runtime `load_skill` tool — separate runtime PR (D4); this slice mounts the ConfigMap as a forward-compatible affordance only

## Snyk

- Code scan: clean on all slice-3 files (`mcp__Snyk__snyk_code_scan` on `operator/`)
- SBOM scans: see comment below

## Test plan

- [x] `go test ./operator/...` passes
- [x] `go build ./operator/...` succeeds (binary + all packages)
- [x] All `TestHandlerReconciler_*` and `TestAssembleSystemPrompt_*` tests pass
- [x] Hash-stability tests prove rollout hash sensitivity to skill content + skill order
- [x] Lazy ConfigMap lifecycle (create / delete) verified across two reconciles
EOF
)"
```

- [ ] **Step 3: Post SBOM summary comment**

Compute the current UTC timestamp (format: `YYYY-MM-DDTHH:MM:SSZ`) and post:

```bash
gh pr comment "$(gh pr view --json url --jq '.url')" --body "$(cat <<'EOF'
## SBOM Summary

| Module | Components | Flagged |
|---|---|---|
| adapters | <count from Task 21 Step 1> | <count or "none"> |
| operator | <count from Task 21 Step 2> | <count or "none"> |
| task-service | <count from Task 21 Step 3> | <count or "none"> |

Generated via Snyk CycloneDX 1.4 — <ISO-8601 timestamp>
EOF
)"
```

Replace `<count>` placeholders and the timestamp with actual values.

---

## Self-Review Against Spec

### Spec coverage check

| Spec requirement (§1 Slice 3 + §2 cross-slice + §3.2 tests + §4 D-entries) | Task |
|---|---|
| `resolvedDependencies` struct (§2.1) | Task 10 |
| Population order: handler-direct → skills → skill-pulled tools, dedup by `KapeTool.Name`, sort for hash (§2.1) | Task 12 |
| `validateDependencies` extends with skills + skill-pulled tools | Task 12 |
| `KapeSkillNotFound` + `KapeSkillNotReady` reasons added (§2.3) | Tasks 10 + 12 |
| `computeRolloutHash` extends with sorted tools + ordered skill Specs (§2.1 lines 3-4) | Task 13 |
| Slice 5 hash extension (cfg.KapeproxyImage*) explicitly NOT added here (§2.1) | Task 13 (note in code comment) |
| Skills NOT sorted — declaration order in hash + prompt (D13) | Task 13 + Task 12 + system_prompt assembly |
| `kape.io/skill-ref-{name}=true` per skill ref (D7) | Task 14 Step 1 |
| `kape.io/tool-ref-{name}=true` for ALL unioned tools (D8) | Task 14 Step 1 |
| `kape-skills-{name}` ConfigMap created when lazy skills exist | Task 14 Step 1 (Step 6 of reconcile) + Task 4 (adapter) |
| `kape-skills-{name}` ConfigMap deleted when none exist | Task 14 Step 1 (else branch) + Task 4 (adapter) |
| `/etc/kape/skills` mount conditional on lazy skills present | Task 9 |
| `AssembleSystemPrompt` pure function | Task 7 |
| `toml.Renderer.Render` calls `AssembleSystemPrompt` | Task 8 |
| `Ready` rollup forward-compatible (negative form) per §2.3 | Task 14 Step 2 (`computeReadyRollup`) |
| Unit: `AssembleSystemPrompt` deterministic | Task 17 (`TestAssembleSystemPrompt_DeterministicForSameInputs`) |
| Unit: `unionToolMap` dedup + sort | Task 19 (`TestHandlerReconciler_LabelSync_TransitiveToolAndSkillLabels`) — proves dedup + label coverage at integration level; `unionToolMap` itself is a 4-line helper exercised exhaustively by the integration tests |
| Unit: `computeRolloutHash` changes on skill reorder + content change | Task 19 (`TestComputeRolloutHash_ChangesWhenSkillSpecChanges` + `TestComputeRolloutHash_ChangesWhenSkillOrderChanges`) |
| envtest: eager+lazy mix → settings.toml correct + ConfigMap with right files | Task 18 + Task 19 (`TestHandlerReconciler_LazySkill_CreatesKapeSkillsConfigMapAndMount`) |
| envtest: only-eager → no lazy ConfigMap | Task 19 (`TestHandlerReconciler_OnlyEagerSkills_NoKapeSkillsConfigMap`) |
| envtest: skill not Ready → handler Pending | Task 19 (`TestHandlerReconciler_SkillNotReady_RequeuePending`) |
| Snyk Code scan clean | Task 20 |
| SBOM scans (3 modules) | Task 21 |
| PR raised with SBOM comment | Task 22 |
| D4: load_skill runtime tool out of scope | Plan goal statement + PR description "Out of scope" |

### Type consistency check

- `SkillRef.Ref` (Task 1) → consumed in `validateDependencies` (Task 12) and label sync (Task 14) — same field name ✓
- `resolvedDependencies` fields (Task 10) → `Schema`, `Tools`, `Skills`, `ToolMap` — referenced consistently in Tasks 12, 13, 14 ✓
- `EagerSkills()` / `LazySkills()` methods (Task 10) → called in `Reconcile` Task 14 Step 1 ✓
- `AssembleSystemPrompt(handler, eager, lazy)` signature (Task 7) → matches call in `toml.Renderer.Render` (Task 8) ✓
- `TOMLRenderer.Render(handler, schema, tools, eagerSkills, lazySkills, cfg)` (Task 5 port + Task 8 impl) → matches call in `Reconcile` Task 14 ✓
- `DeploymentPort.Ensure(..., lazySkillsPresent bool)` (Task 6 port + Task 9 impl) → matches call in `Reconcile` Task 14 ✓
- `SkillConfigMapPort.Ensure(handler, lazySkills)` + `.Delete(handler)` (Task 3 port + Task 4 impl) → matches calls in `Reconcile` Task 14 ✓
- `NewHandlerReconciler(handlers, schemas, tools, skills, configMaps, skillConfigMaps, serviceAccounts, deployments, scaledObjects, tomlRenderer, kapeConfig)` (Task 11) → matches caller in Task 15 (`controller/handler.go`) and Task 16 (`cmd/main.go`) and Task 19 (`newReconciler` helper) ✓
- `SkillConfigMapName(handlerName)` (Task 4) → referenced in Task 9 deployment volume + Task 19 test getter ✓
- `ReasonKapeSkillNotFound` / `ReasonKapeSkillNotReady` (Task 10) → returned from `validateDependencies` (Task 12), asserted in tests (Task 19) ✓

### Placeholder scan

- No "TBD"/"TODO"/"implement later" text — every code block is complete
- One annotated alternative is in Task 8 (import-cycle fallback). It's marked clearly and only kicks in if circular imports appear; if they don't (the default), no action needed
- All commit commands include the explicit worktree path
- Task 15 Step 2 includes a concrete example shape because the existing controller wiring layout varies by codebase; the engineer applies the minimal edit that compiles. Acceptable per "exact paths and minimal example" rule for adapter wiring tasks

### Out-of-scope confirmation

- Slice 4 watch wiring: NOT touched — only labels are written ✓
- Slice 5 kapeproxy: NOT touched — sidecar logic in `buildSidecars` left unchanged (still emits `kapetool-*` containers); slice 5 reshapes ✓
- Slice 6 `KapeProxyReady`: NOT touched — `Ready` rollup is forward-compatible so slice 6 needs no edits here ✓
- KapeSkill status writes: NOT touched — slice 1 owns the `Ready` reconciliation on `KapeSkill` ✓
- CEL admission rules: NOT introduced — D5 ✓
- `load_skill` runtime tool: NOT added — D4 ✓

---

## Definition of Done (slice 3, from spec)

| DoD criterion | Demonstrated by |
|---|---|
| Apply KapeHandler + KapeSkill (lazyLoad: true) → `kape-skills-{name}` ConfigMap exists, mounted at `/etc/kape/skills/` | `TestHandlerReconciler_LazySkill_CreatesKapeSkillsConfigMapAndMount` (Task 19) |
| KapeSkill with not-Ready KapeTool puts KapeHandler in Pending with reason `KapeSkillNotReady` | `TestHandlerReconciler_SkillNotReady_RequeuePending` (Task 19) |
| Eager-skill instruction text appears in settings.toml in declaration order | `TestRender_SystemPromptIncludesEagerSkillInDeclarationOrder` (Task 18) + `TestAssembleSystemPrompt_HandlerPlusEager_MultipleInDeclarationOrder` (Task 17) |
| `computeRolloutHash` changes when a referenced KapeSkill's `.Spec` content changes | `TestComputeRolloutHash_ChangesWhenSkillSpecChanges` (Task 19) |
| Snyk Code scan clean | Task 20 |
| SBOM scans (3 modules) | Task 21 |
| PR raised with SBOM comment | Task 22 |
