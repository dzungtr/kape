# Phase 6 Slice 1 — KapeSkill CRD + KapeSkillReconciler Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce the `KapeSkill` CRD (types, deepcopy, generated CRD manifest), a `SkillRepository` port + adapter, and the `KapeSkillReconciler` — covering both the Happy path (Ready KapeTool → skill Ready) and deletion protection (finalizer blocks delete while any KapeHandler references the skill).

**Architecture:** Mirrors the established `KapeSchema` / `KapeTool` pattern: thin CRD types in `v1alpha1`, port interface in `infra/ports`, concrete k8s adapter in `infra/k8s`, pure reconcile logic in `controller/reconcile`, controller-runtime wiring in `controller/`, and registration in `cmd/main.go`. No CEL cross-field rules at v1alpha1 (spec decision D5). Deletion protection uses label `kape.io/skill-ref-{name}=true` on KapeHandlers (mirroring the tool pattern); the `HandlerRepository` already has a `ListHandlersByToolRef` method whose counterpart for skills is added in this slice.

**Tech Stack:** Go 1.25, controller-runtime (sigs.k8s.io/controller-runtime), kubebuilder markers, controller-gen (via `make generate` at repo root), envtest (sigs.k8s.io/controller-runtime/pkg/envtest), testify

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| **Create** | `operator/infra/api/v1alpha1/kapeskill_types.go` | `KapeSkill`, `KapeSkillSpec`, `KapeSkillStatus`, `KapeSkillList`, kubebuilder markers |
| **Regenerate** | `operator/infra/api/v1alpha1/zz_generated.deepcopy.go` | DeepCopy for all v1alpha1 types (run via `make generate`) |
| **Regenerate** | `crds/kape.io_kapeskills.yaml` | CRD manifest (run via `make generate`) |
| **Create** | `operator/infra/ports/skill.go` | `SkillRepository` port interface |
| **Modify** | `operator/infra/ports/handler.go` | Add `ListHandlersBySkillRef` to `HandlerRepository` interface |
| **Create** | `operator/infra/k8s/skill_repo.go` | `SkillRepository` k8s adapter |
| **Modify** | `operator/infra/k8s/handler_repo.go` | Implement `ListHandlersBySkillRef` on `HandlerRepository` |
| **Create** | `operator/controller/reconcile/skill.go` | `SkillReconciler`: `Reconcile`, `validateSkillSpec`, `handleDeletion` |
| **Create** | `operator/controller/skill.go` | `KapeSkillReconciler` (controller-runtime adapter) + `SetupSkillReconciler` |
| **Modify** | `operator/cmd/main.go` | Instantiate adapters + register `SkillReconciler` |
| **Create** | `operator/controller/reconcile/skill_test.go` | Unit + fake-client tests for all reconcile scenarios |

---

## Task 1: Define KapeSkill types

**Files:**
- Create: `operator/infra/api/v1alpha1/kapeskill_types.go`

- [ ] **Step 1: Write the type file**

```go
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SkillToolRef references a KapeTool by name within a KapeSkill.
type SkillToolRef struct {
	// Ref is the name of the KapeTool in the same namespace.
	// +kubebuilder:validation:MinLength=1
	Ref string `json:"ref"`
}

// KapeSkillSpec defines the desired state of a KapeSkill.
type KapeSkillSpec struct {
	// Description is a human-readable summary of what this skill does.
	// +kubebuilder:validation:MinLength=1
	Description string `json:"description"`

	// Instruction is the system-prompt text injected when this skill is eager-loaded.
	// +kubebuilder:validation:MinLength=1
	Instruction string `json:"instruction"`

	// Tools is the list of KapeTools this skill requires.
	// +optional
	Tools []SkillToolRef `json:"tools,omitempty"`

	// LazyLoad defers injection of this skill's instruction until explicitly invoked.
	// When false (default), the instruction is included in the handler's system prompt at startup.
	// +optional
	// +kubebuilder:default=false
	LazyLoad bool `json:"lazyLoad,omitempty"`
}

// KapeSkillStatus defines the observed state of a KapeSkill.
type KapeSkillStatus struct {
	// Conditions represent the latest available observations of the skill's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// KapeSkill groups a reusable instruction + tool set that can be attached to one or
// more KapeHandlers.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=kape
// +kubebuilder:printcolumn:name="LazyLoad",type=boolean,JSONPath=`.spec.lazyLoad`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type KapeSkill struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KapeSkillSpec   `json:"spec,omitempty"`
	Status KapeSkillStatus `json:"status,omitempty"`
}

// KapeSkillList contains a list of KapeSkill resources.
//
// +kubebuilder:object:root=true
type KapeSkillList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []KapeSkill `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KapeSkill{}, &KapeSkillList{})
}
```

- [ ] **Step 2: Verify the file compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./infra/api/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan add operator/infra/api/v1alpha1/kapeskill_types.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan commit -m "feat(api): add KapeSkill/KapeSkillSpec/KapeSkillStatus CRD types"
```

---

## Task 2: Regenerate DeepCopy and CRD manifest

**Files:**
- Regenerate: `operator/infra/api/v1alpha1/zz_generated.deepcopy.go`
- Regenerate: `crds/kape.io_kapeskills.yaml`

> **Note:** Run these from the **repo root** (`/home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan`), not from the `operator/` subdirectory.

- [ ] **Step 1: Run make generate**

```bash
cd /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan && make generate
```

Expected output includes lines like:
```
Generating deepcopy funcs
```

The command runs `controller-gen` against `./operator/infra/...` and writes:
- `operator/infra/api/v1alpha1/zz_generated.deepcopy.go` (updated with `DeepCopyObject`, `DeepCopyInto` for `KapeSkill`, `KapeSkillList`, `KapeSkillSpec`, `KapeSkillStatus`, `SkillToolRef`)
- `crds/kape.io_kapeskills.yaml` (new file)

- [ ] **Step 2: Confirm the CRD YAML was created**

```bash
ls /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan/crds/kape.io_kapeskills.yaml
```

Expected: file exists.

- [ ] **Step 3: Spot-check deepcopy**

```bash
grep -n "KapeSkill\|SkillToolRef" /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan/operator/infra/api/v1alpha1/zz_generated.deepcopy.go
```

Expected: `DeepCopyInto` functions for `KapeSkill`, `KapeSkillList`, `KapeSkillSpec`, `KapeSkillStatus`, `SkillToolRef`.

- [ ] **Step 4: Commit generated files**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan add \
  operator/infra/api/v1alpha1/zz_generated.deepcopy.go \
  crds/kape.io_kapeskills.yaml
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan commit -m "chore: regenerate deepcopy and CRD manifest for KapeSkill"
```

---

## Task 3: Add SkillRepository port + extend HandlerRepository port

**Files:**
- Create: `operator/infra/ports/skill.go`
- Modify: `operator/infra/ports/handler.go`

- [ ] **Step 1: Check current HandlerRepository interface**

```bash
cat /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan/operator/infra/ports/handler.go
```

Note the existing methods (Get, UpdateStatus, AddFinalizer, RemoveFinalizer, ListBy*). You will add `ListHandlersBySkillRef` to this interface.

- [ ] **Step 2: Create skill port**

```go
// operator/infra/ports/skill.go
package ports

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// SkillRepository reads and writes KapeSkill resources.
type SkillRepository interface {
	// Get fetches a KapeSkill by namespaced name. Returns nil, nil when not found.
	Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeSkill, error)

	// UpdateStatus persists status sub-resource changes.
	UpdateStatus(ctx context.Context, skill *v1alpha1.KapeSkill) error

	// AddFinalizer adds the given finalizer string to the skill if not already present.
	AddFinalizer(ctx context.Context, skill *v1alpha1.KapeSkill, finalizer string) error

	// RemoveFinalizer removes the given finalizer string from the skill.
	RemoveFinalizer(ctx context.Context, skill *v1alpha1.KapeSkill, finalizer string) error

	// ListHandlersBySkillRef returns all KapeHandlers with label kape.io/skill-ref-{skillName}=true.
	ListHandlersBySkillRef(ctx context.Context, skillName string) ([]v1alpha1.KapeHandler, error)
}
```

- [ ] **Step 3: Add ListHandlersBySkillRef to HandlerRepository**

Open `operator/infra/ports/handler.go` and add the following method to the `HandlerRepository` interface (after the existing `ListHandlersByToolRef` or at the end of the interface block):

```go
// ListHandlersBySkillRef returns all KapeHandlers with label kape.io/skill-ref-{skillName}=true.
ListHandlersBySkillRef(ctx context.Context, skillName string) ([]v1alpha1.KapeHandler, error)
```

- [ ] **Step 4: Verify compilation**

```bash
cd /home/tony/projects/kape-io/operator && go build ./infra/ports/...
```

Expected: compile error about `HandlerRepository` not fully implemented (the k8s adapter doesn't have `ListHandlersBySkillRef` yet). This is expected — Task 4 fixes it.

- [ ] **Step 5: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan add \
  operator/infra/ports/skill.go \
  operator/infra/ports/handler.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan commit -m "feat(ports): add SkillRepository port and ListHandlersBySkillRef to HandlerRepository"
```

---

## Task 4: Implement SkillRepository adapter + extend HandlerRepository adapter

**Files:**
- Create: `operator/infra/k8s/skill_repo.go`
- Modify: `operator/infra/k8s/handler_repo.go`

- [ ] **Step 1: Check HandlerRepository adapter**

```bash
cat /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan/operator/infra/k8s/handler_repo.go
```

Find the `ListHandlersByToolRef` method — you'll add `ListHandlersBySkillRef` immediately after it with the same pattern.

- [ ] **Step 2: Create skill_repo.go**

```go
// operator/infra/k8s/skill_repo.go
package k8s

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// SkillRepository implements ports.SkillRepository.
type SkillRepository struct {
	client client.Client
}

// NewSkillRepository creates a new SkillRepository.
func NewSkillRepository(c client.Client) *SkillRepository {
	return &SkillRepository{client: c}
}

// Get fetches a KapeSkill by namespaced name. Returns nil, nil when not found.
func (r *SkillRepository) Get(ctx context.Context, key types.NamespacedName) (*v1alpha1.KapeSkill, error) {
	var skill v1alpha1.KapeSkill
	if err := r.client.Get(ctx, key, &skill); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	return &skill, nil
}

// UpdateStatus persists the skill's status sub-resource using RetryOnConflict.
func (r *SkillRepository) UpdateStatus(ctx context.Context, skill *v1alpha1.KapeSkill) error {
	key := types.NamespacedName{Name: skill.Name, Namespace: skill.Namespace}
	desired := skill.Status
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest v1alpha1.KapeSkill
		if err := r.client.Get(ctx, key, &latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		latest.Status = desired
		return r.client.Status().Update(ctx, &latest)
	})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("updating KapeSkill %s/%s status: %w", skill.Namespace, skill.Name, err)
	}
	return nil
}

// AddFinalizer adds the given finalizer to the skill if not already present.
func (r *SkillRepository) AddFinalizer(ctx context.Context, skill *v1alpha1.KapeSkill, finalizer string) error {
	if controllerutil.ContainsFinalizer(skill, finalizer) {
		return nil
	}
	patch := client.MergeFrom(skill.DeepCopy())
	controllerutil.AddFinalizer(skill, finalizer)
	if err := r.client.Patch(ctx, skill, patch); err != nil {
		return fmt.Errorf("adding finalizer to KapeSkill %s/%s: %w", skill.Namespace, skill.Name, err)
	}
	return nil
}

// RemoveFinalizer removes the given finalizer from the skill.
func (r *SkillRepository) RemoveFinalizer(ctx context.Context, skill *v1alpha1.KapeSkill, finalizer string) error {
	if !controllerutil.ContainsFinalizer(skill, finalizer) {
		return nil
	}
	patch := client.MergeFrom(skill.DeepCopy())
	controllerutil.RemoveFinalizer(skill, finalizer)
	if err := r.client.Patch(ctx, skill, patch); err != nil {
		return fmt.Errorf("removing finalizer from KapeSkill %s/%s: %w", skill.Namespace, skill.Name, err)
	}
	return nil
}

// ListHandlersBySkillRef returns KapeHandlers with label kape.io/skill-ref-{skillName}=true.
func (r *SkillRepository) ListHandlersBySkillRef(ctx context.Context, skillName string) ([]v1alpha1.KapeHandler, error) {
	var list v1alpha1.KapeHandlerList
	if err := r.client.List(ctx, &list, client.MatchingLabels{
		"kape.io/skill-ref-" + skillName: "true",
	}); err != nil {
		return nil, fmt.Errorf("listing handlers by skill ref %q: %w", skillName, err)
	}
	return list.Items, nil
}
```

- [ ] **Step 3: Add ListHandlersBySkillRef to handler_repo.go**

In `operator/infra/k8s/handler_repo.go`, add this method to the `HandlerRepository` struct (after `ListHandlersByToolRef`):

```go
// ListHandlersBySkillRef returns KapeHandlers with label kape.io/skill-ref-{skillName}=true.
func (r *HandlerRepository) ListHandlersBySkillRef(ctx context.Context, skillName string) ([]v1alpha1.KapeHandler, error) {
	var list v1alpha1.KapeHandlerList
	if err := r.client.List(ctx, &list, client.MatchingLabels{
		"kape.io/skill-ref-" + skillName: "true",
	}); err != nil {
		return nil, fmt.Errorf("listing handlers by skill ref %q: %w", skillName, err)
	}
	return list.Items, nil
}
```

- [ ] **Step 4: Verify compilation**

```bash
cd /home/tony/projects/kape-io/operator && go build ./infra/...
```

Expected: no errors.

- [ ] **Step 5: Run existing k8s adapter tests**

```bash
cd /home/tony/projects/kape-io/operator && go test ./infra/k8s/... -v 2>&1 | tail -20
```

Expected: all existing tests pass.

- [ ] **Step 6: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan add \
  operator/infra/k8s/skill_repo.go \
  operator/infra/k8s/handler_repo.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan commit -m "feat(k8s): add SkillRepository adapter and ListHandlersBySkillRef on HandlerRepository"
```

---

## Task 5: Implement SkillReconciler

**Files:**
- Create: `operator/controller/reconcile/skill.go`

The reconcile loop mirrors `schema.go`. Key differences:
- The skill fetches each tool from `spec.tools[].ref` and checks it is Ready.
- Finalizer: `kape.io/skill-protection`.
- `ListHandlersBySkillRef` (via `SkillRepository`) checks deletion safety.
- Condition type: `Ready`.

- [ ] **Step 1: Write skill.go**

```go
// operator/controller/reconcile/skill.go
package reconcile

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

const skillFinalizer = "kape.io/skill-protection"

// SkillReconciler performs the full reconcile logic for KapeSkill.
type SkillReconciler struct {
	skills ports.SkillRepository
	tools  ports.ToolRepository
}

// NewSkillReconciler creates a SkillReconciler.
func NewSkillReconciler(skills ports.SkillRepository, tools ports.ToolRepository) *SkillReconciler {
	return &SkillReconciler{skills: skills, tools: tools}
}

// Reconcile implements the KapeSkill reconcile loop.
func (r *SkillReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	skill, err := r.skills.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeSkill: %w", err)
	}
	if skill == nil {
		return ctrl.Result{}, nil
	}

	// 1. Validate spec fields
	if err := validateSkillSpec(skill); err != nil {
		skill.Status.Conditions = setCondition(skill.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidSpec",
			Message: err.Error(),
		})
		_ = r.skills.UpdateStatus(ctx, skill)
		return ctrl.Result{}, nil // terminal
	}

	// 2. Manage finalizer
	if err := r.skills.AddFinalizer(ctx, skill, skillFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
	}

	// 3. Handle deletion
	if !skill.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, skill)
	}

	// 4. Resolve each referenced KapeTool
	for _, ref := range skill.Spec.Tools {
		toolKey := types.NamespacedName{Name: ref.Ref, Namespace: skill.Namespace}
		tool, err := r.tools.Get(ctx, toolKey)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("fetching KapeTool %q: %w", ref.Ref, err)
		}
		if tool == nil {
			skill.Status.Conditions = setCondition(skill.Status.Conditions, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "KapeToolNotFound",
				Message: fmt.Sprintf("KapeTool %q not found", ref.Ref),
			})
			_ = r.skills.UpdateStatus(ctx, skill)
			return ctrl.Result{RequeueAfter: 30 * 1e9}, nil // 30s
		}
		if !isReady(tool.Status.Conditions) {
			skill.Status.Conditions = setCondition(skill.Status.Conditions, metav1.Condition{
				Type:    "Ready",
				Status:  metav1.ConditionFalse,
				Reason:  "KapeToolNotReady",
				Message: fmt.Sprintf("KapeTool %q is not Ready", ref.Ref),
			})
			_ = r.skills.UpdateStatus(ctx, skill)
			return ctrl.Result{RequeueAfter: 30 * 1e9}, nil // 30s
		}
	}

	// 5. All tools ready (or no tools) — set Ready=True
	skill.Status.Conditions = setCondition(skill.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Ready",
		Message: "All referenced tools are ready",
	})
	if err := r.skills.UpdateStatus(ctx, skill); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SkillReconciler) handleDeletion(ctx context.Context, skill *v1alpha1.KapeSkill) (ctrl.Result, error) {
	handlers, err := r.skills.ListHandlersBySkillRef(ctx, skill.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing handlers for skill %q: %w", skill.Name, err)
	}
	if len(handlers) > 0 {
		names := make([]string, 0, len(handlers))
		for _, h := range handlers {
			names = append(names, h.Name)
		}
		skill.Status.Conditions = setCondition(skill.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ReferencedByHandlers",
			Message: fmt.Sprintf("Cannot delete: referenced by handlers: [%s]", strings.Join(names, ", ")),
		})
		_ = r.skills.UpdateStatus(ctx, skill)
		return ctrl.Result{}, nil // blocked — re-triggered on handler deletion
	}
	if err := r.skills.RemoveFinalizer(ctx, skill, skillFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func validateSkillSpec(skill *v1alpha1.KapeSkill) error {
	if skill.Spec.Description == "" {
		return fmt.Errorf("spec.description must not be empty")
	}
	if skill.Spec.Instruction == "" {
		return fmt.Errorf("spec.instruction must not be empty")
	}
	return nil
}

// isReady returns true when a Ready=True condition is present in the condition slice.
func isReady(conditions []metav1.Condition) bool {
	for _, c := range conditions {
		if c.Type == "Ready" && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}
```

> **Note on RequeueAfter:** `ctrl.Result{RequeueAfter: 30 * 1e9}` uses nanoseconds (`time.Duration`). Import `"time"` and use `30 * time.Second` instead if you prefer. Both compile; the plan uses the explicit nanosecond form here but the idiomatic import form is preferred:

```go
import "time"
// ...
return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
```

- [ ] **Step 2: Fix RequeueAfter to use time.Second**

Update the two `RequeueAfter` lines in `skill.go` to:

```go
return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
```

Add `"time"` to the import block.

- [ ] **Step 3: Verify compilation**

```bash
cd /home/tony/projects/kape-io/operator && go build ./controller/reconcile/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan add \
  operator/controller/reconcile/skill.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan commit -m "feat(reconcile): implement SkillReconciler with finalizer and tool-readiness gate"
```

---

## Task 6: Wire controller + register in main.go

**Files:**
- Create: `operator/controller/skill.go`
- Modify: `operator/cmd/main.go`

- [ ] **Step 1: Create controller wiring**

```go
// operator/controller/skill.go
package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

// KapeSkillReconciler is the thin controller-runtime adapter for KapeSkill.
type KapeSkillReconciler struct {
	inner *reconcile.SkillReconciler
}

// NewKapeSkillReconciler creates a KapeSkillReconciler.
func NewKapeSkillReconciler(inner *reconcile.SkillReconciler) *KapeSkillReconciler {
	return &KapeSkillReconciler{inner: inner}
}

// Reconcile implements reconcile.Reconciler.
func (r *KapeSkillReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.inner.Reconcile(ctx, req.NamespacedName)
}

// SetupSkillReconciler registers the KapeSkill reconciler with the controller manager.
func SetupSkillReconciler(mgr manager.Manager, inner *reconcile.SkillReconciler, maxConcurrent int) error {
	r := NewKapeSkillReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeSkill{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
```

- [ ] **Step 2: Register in main.go**

In `operator/cmd/main.go`, add the following after the `toolRepo` and `schemaRepo` adapter declarations (near line 86), in the Adapters block:

```go
skillRepo := k8sadapters.NewSkillRepository(k8sClient)
```

Then add the following reconciler setup block after the KapeSchemaReconciler block (before the KapeHandlerReconciler block):

```go
// KapeSkillReconciler
skillRec := reconcilehandler.NewSkillReconciler(skillRepo, toolRepo)
if err := kapecontroller.SetupSkillReconciler(mgr, skillRec, cfg.MaxConcurrentReconciles); err != nil {
    log.Error(err, "setting up KapeSkill controller")
    os.Exit(1)
}
```

- [ ] **Step 3: Build the operator binary**

```bash
cd /home/tony/projects/kape-io/operator && go build ./cmd/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan add \
  operator/controller/skill.go \
  operator/cmd/main.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan commit -m "feat(controller): wire KapeSkillReconciler and register in main"
```

---

## Task 7: Write tests for SkillReconciler

**Files:**
- Create: `operator/controller/reconcile/skill_test.go`

Tests use `sigs.k8s.io/controller-runtime/pkg/client/fake` (same as `schema_test.go`). No envtest binary required — the fake client handles status subresources via `WithStatusSubresource`. The `findCondition` helper already exists in `tool_test.go`; you can reference it since all `reconcile_test` package files share it.

- [ ] **Step 1: Write the test file**

```go
// operator/controller/reconcile/skill_test.go
package reconcile_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	k8sadapters "github.com/kape-io/kape/operator/infra/k8s"
	"github.com/kape-io/kape/operator/controller/reconcile"
)

// ─── helpers ────────────────────────────────────────────────────────────────

func newSkillScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	return s
}

func validSkill(name, ns string) *v1alpha1.KapeSkill {
	return &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: v1alpha1.KapeSkillSpec{
			Description: "A test skill",
			Instruction: "You are a test skill.",
		},
	}
}

func skillWithTool(name, ns, toolRef string) *v1alpha1.KapeSkill {
	s := validSkill(name, ns)
	s.Spec.Tools = []v1alpha1.SkillToolRef{{Ref: toolRef}}
	return s
}

func readyToolForSkill(name, ns string) *v1alpha1.KapeTool {
	return &v1alpha1.KapeTool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       v1alpha1.KapeToolSpec{Type: "mcp", MCP: &v1alpha1.MCPSpec{Upstream: v1alpha1.MCPUpstreamSpec{Transport: "sse", URL: "http://mcp:8080"}}},
		Status: v1alpha1.KapeToolStatus{
			Conditions: []metav1.Condition{{
				Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready",
			}},
		},
	}
}

func newSkillReconciler(c interface{ GetScheme() *runtime.Scheme }, objs ...interface{}) *reconcile.SkillReconciler {
	// Use the objects in the fake client passed to the function.
	// This helper is a no-op — callers build the fake client and pass the adapters.
	panic("use newSkillReconcilerFromClient instead")
}

// ─── tests ───────────────────────────────────────────────────────────────────

func TestSkillReconciler_NoTools_SetsReady(t *testing.T) {
	skill := validSkill("my-skill", "kape-system")
	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "Ready", cond.Reason)
}

func TestSkillReconciler_ReadyTool_SetsReady(t *testing.T) {
	skill := skillWithTool("my-skill", "kape-system", "grafana-mcp")
	tool := readyToolForSkill("grafana-mcp", "kape-system")

	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill, tool).
		WithStatusSubresource(skill, tool).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
}

func TestSkillReconciler_MissingTool_SetsNotReady(t *testing.T) {
	skill := skillWithTool("my-skill", "kape-system", "missing-tool")

	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, result.RequeueAfter)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "KapeToolNotFound", cond.Reason)
}

func TestSkillReconciler_NotReadyTool_SetsNotReady(t *testing.T) {
	skill := skillWithTool("my-skill", "kape-system", "grafana-mcp")
	tool := readyToolForSkill("grafana-mcp", "kape-system")
	tool.Status.Conditions[0].Status = metav1.ConditionFalse // not ready

	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill, tool).
		WithStatusSubresource(skill, tool).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, result.RequeueAfter)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "KapeToolNotReady", cond.Reason)
}

func TestSkillReconciler_InvalidSpec_SetsInvalidSpec(t *testing.T) {
	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-skill", Namespace: "kape-system"},
		Spec: v1alpha1.KapeSkillSpec{
			Description: "",      // missing
			Instruction: "test",
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	result, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "bad-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	assert.False(t, result.Requeue)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "bad-skill", Namespace: "kape-system"})
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "InvalidSpec", cond.Reason)
}

func TestSkillReconciler_FinalizerAddedOnCreate(t *testing.T) {
	skill := validSkill("my-skill", "kape-system")
	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	assert.Contains(t, got.Finalizers, "kape.io/skill-protection")
}

func TestSkillReconciler_DeletionBlockedWhenHandlerReferences(t *testing.T) {
	now := metav1.Now()
	skill := validSkill("my-skill", "kape-system")
	skill.DeletionTimestamp = &now
	skill.Finalizers = []string{"kape.io/skill-protection"}

	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-handler",
			Namespace: "kape-system",
			Labels:    map[string]string{"kape.io/skill-ref-my-skill": "true"},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill, handler).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	// Finalizer must still be present (deletion blocked)
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	require.NotNil(t, got)
	assert.Contains(t, got.Finalizers, "kape.io/skill-protection")
	cond := findCondition(got.Status.Conditions, "Ready")
	require.NotNil(t, cond)
	assert.Equal(t, "ReferencedByHandlers", cond.Reason)
}

func TestSkillReconciler_DeletionUnblockedWhenNoHandlerReferences(t *testing.T) {
	now := metav1.Now()
	skill := validSkill("my-skill", "kape-system")
	skill.DeletionTimestamp = &now
	skill.Finalizers = []string{"kape.io/skill-protection"}

	c := fake.NewClientBuilder().
		WithScheme(newSkillScheme()).
		WithObjects(skill).
		WithStatusSubresource(skill).
		Build()

	r := reconcile.NewSkillReconciler(
		k8sadapters.NewSkillRepository(c),
		k8sadapters.NewToolRepository(c),
	)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})

	require.NoError(t, err)
	// Finalizer must be removed when no handlers reference it
	got, _ := k8sadapters.NewSkillRepository(c).Get(context.Background(), types.NamespacedName{Name: "my-skill", Namespace: "kape-system"})
	// After finalizer removal the object may be gone; check nil or no finalizer
	if got != nil {
		assert.NotContains(t, got.Finalizers, "kape.io/skill-protection")
	}
}
```

> **Note:** Remove the `newSkillReconciler` panic-stub helper from the file — it was a placeholder included for clarity. The actual tests call `reconcile.NewSkillReconciler(...)` directly.

- [ ] **Step 2: Run tests to see them fail (TDD gate)**

```bash
cd /home/tony/projects/kape-io/operator && go test ./controller/reconcile/... -run TestSkill -v 2>&1 | tail -30
```

Expected: compile errors (`NewSkillRepository` not found, `SkillRepository` not defined). This confirms the test is wired to real code that exists after Task 4.

- [ ] **Step 3: Run all reconcile tests**

```bash
cd /home/tony/projects/kape-io/operator && go test ./controller/reconcile/... -v 2>&1 | tail -40
```

Expected: all tests pass, including the new `TestSkill*` tests.

- [ ] **Step 4: Commit**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan add \
  operator/controller/reconcile/skill_test.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan commit -m "test(reconcile): add SkillReconciler unit tests covering all DoD scenarios"
```

---

## Task 8: Run full operator test suite

- [ ] **Step 1: Run all operator tests**

```bash
cd /home/tony/projects/kape-io/operator && go test ./... 2>&1 | tail -30
```

Expected: `ok` for all packages. No failures.

- [ ] **Step 2: Build all operator binaries**

```bash
cd /home/tony/projects/kape-io/operator && go build ./...
```

Expected: no errors.

---

## Task 9: Snyk Code scan + fix any issues

> Use the `mcp__Snyk__snyk_code_scan` MCP tool (not the `snyk` CLI).

- [ ] **Step 1: Run Snyk Code scan on operator/**

Call `mcp__Snyk__snyk_code_scan` with path `operator/` from the worktree root:

```
tool: mcp__Snyk__snyk_code_scan
args: { "path": "/home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan/operator" }
```

- [ ] **Step 2: Evaluate results**

If the scan returns issues **only in pre-existing files** (not in the files introduced by this slice), those are pre-existing and do not block the PR.

If the scan returns issues **in any file modified or created by this slice** (`kapeskill_types.go`, `skill.go`, `skill_repo.go`, `skill_test.go`, `skill.go` controller, `main.go`), fix them and re-scan.

- [ ] **Step 3: Re-scan after fixes (if any)**

```
tool: mcp__Snyk__snyk_code_scan
args: { "path": "/home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan/operator" }
```

Expected: no new issues in slice-1 files.

- [ ] **Step 4: Commit any fixes**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan add -u
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan commit -m "fix(security): address Snyk Code findings in KapeSkill files"
```

(Skip this step if there were no issues to fix.)

---

## Task 10: SBOM scans (per kape-io CLAUDE.md PR checklist)

> Use `mcp__Snyk__snyk_sbom_scan` with format `cyclonedx1.4+json`.

- [ ] **Step 1: Run SBOM scan on adapters**

```
tool: mcp__Snyk__snyk_sbom_scan
args: {
  "path": "/home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan/adapters",
  "format": "cyclonedx1.4+json"
}
```

Record: component count, any flagged packages.

- [ ] **Step 2: Run SBOM scan on operator**

```
tool: mcp__Snyk__snyk_sbom_scan
args: {
  "path": "/home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan/operator",
  "format": "cyclonedx1.4+json"
}
```

Record: component count, any flagged packages.

- [ ] **Step 3: Run SBOM scan on task-service**

```
tool: mcp__Snyk__snyk_sbom_scan
args: {
  "path": "/home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan/task-service",
  "format": "cyclonedx1.4+json"
}
```

Record: component count, any flagged packages.

---

## Task 11: Push branch, open PR, post SBOM comment

- [ ] **Step 1: Push the branch**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice1-plan push -u origin docs/phase6-slice1-plan
```

- [ ] **Step 2: Open the PR**

```bash
gh pr create \
  --repo dzungtr/kape \
  --base main \
  --head docs/phase6-slice1-plan \
  --title "feat(phase6): KapeSkill CRD types + KapeSkillReconciler (slice 1)" \
  --body "$(cat <<'EOF'
## Summary

- Adds `KapeSkill` / `KapeSkillSpec` / `KapeSkillStatus` types to `v1alpha1` with kubebuilder markers
- Adds `SkillRepository` port + k8s adapter; extends `HandlerRepository` with `ListHandlersBySkillRef`
- Implements `SkillReconciler` with `kape.io/skill-protection` finalizer and per-tool readiness gate
- Wires `KapeSkillReconciler` controller and registers in `cmd/main.go`
- Regenerates `zz_generated.deepcopy.go` and `crds/kape.io_kapeskills.yaml`

## Acceptance criteria (from Phase 6 README)

- [x] Apply KapeSkill referencing a Ready KapeTool → KapeSkill status shows Ready
- [x] Attempt to delete a KapeSkill referenced by a KapeHandler → deletion blocked

Both demonstrated by tests in `operator/controller/reconcile/skill_test.go`.

## Snyk

- Code scan: clean on all slice-1 files
- SBOM scans: see comment below

## Test plan

- [ ] `go test ./operator/...` passes
- [ ] `go build ./operator/cmd/...` succeeds
- [ ] All `TestSkill*` tests pass
EOF
)"
```

- [ ] **Step 3: Post SBOM summary comment**

After running the three SBOM scans in Task 10, compute the current UTC timestamp (format: `2026-05-09T<HH:MM:SS>Z`) and post:

```bash
gh pr comment "$(gh pr view --json url --jq '.url')" --body "$(cat <<'EOF'
## SBOM Summary

| Module | Components | Flagged |
|---|---|---|
| adapters | <count from Task 10 Step 1> | <count or "none"> |
| operator | <count from Task 10 Step 2> | <count or "none"> |
| task-service | <count from Task 10 Step 3> | <count or "none"> |

Generated via Snyk CycloneDX 1.4 — 2026-05-09T00:00:00Z
EOF
)"
```

Replace `<count>` placeholders with actual values from the scan results. Replace the timestamp with the actual current UTC time when posting.

---

## Self-Review Against Spec

### Spec coverage check

| Spec requirement | Task covering it |
|---|---|
| `kapeskill_types.go` with `KapeSkill`/`KapeSkillSpec`/`KapeSkillStatus`, kubebuilder markers | Task 1 |
| No CEL cross-field rules at v1alpha1 (D5) | Task 1 — no `XValidation` rules on spec |
| Regenerate `zz_generated.deepcopy.go` and `crds/kape.io_kapeskills.yaml` | Task 2 |
| `SkillRepository` port at `operator/infra/ports/skill.go` | Task 3 |
| Extend `HandlerRepository` with `ListHandlersBySkillRef` | Tasks 3 + 4 |
| `SkillRepository` adapter at `operator/infra/k8s/skill_repo.go` | Task 4 |
| `SkillReconciler` with `validateSkillSpec`, `handleDeletion`, finalizer `kape.io/skill-protection` | Task 5 |
| `SetupSkillReconciler` in `operator/controller/skill.go` | Task 6 |
| Register in `operator/cmd/main.go` | Task 6 |
| Tests: valid skill + Ready tool → Ready=True | Task 7 `TestSkillReconciler_ReadyTool_SetsReady` |
| Tests: missing tool → Ready=False, KapeToolNotFound | Task 7 `TestSkillReconciler_MissingTool_SetsNotReady` |
| Tests: not-Ready tool → Ready=False, KapeToolNotReady | Task 7 `TestSkillReconciler_NotReadyTool_SetsNotReady` |
| Tests: delete-while-referenced blocked | Task 7 `TestSkillReconciler_DeletionBlockedWhenHandlerReferences` |
| Tests: delete-after-handler-removes-ref unblocks | Task 7 `TestSkillReconciler_DeletionUnblockedWhenNoHandlerReferences` |
| Tests: finalizer added on create | Task 7 `TestSkillReconciler_FinalizerAddedOnCreate` |
| Snyk Code scan clean | Task 9 |
| SBOM scans (adapters, operator, task-service) | Task 10 |
| PR raised with SBOM comment | Task 11 |

### Type consistency check

- `SkillToolRef.Ref` (Task 1) → used in `skill.Spec.Tools[].Ref` (Task 5 reconcile loop) ✓
- `ports.SkillRepository` interface methods (Task 3) → all implemented on `k8s.SkillRepository` (Task 4) ✓
- `ports.HandlerRepository.ListHandlersBySkillRef` (Task 3) → implemented on `k8s.HandlerRepository` (Task 4) ✓
- `reconcile.NewSkillReconciler(skills ports.SkillRepository, tools ports.ToolRepository)` (Task 5) → called in `main.go` as `reconcilehandler.NewSkillReconciler(skillRepo, toolRepo)` (Task 6) ✓
- `controller.SetupSkillReconciler(mgr, skillRec, maxConcurrent)` (Task 6) → matches call site in `main.go` ✓
- Test `k8sadapters.NewSkillRepository(c)` (Task 7) → constructor defined in Task 4 ✓
- Test `findCondition` helper → defined in `tool_test.go` (same package `reconcile_test`) ✓

### Placeholder scan

No TBD, TODO, or "implement later" text. All code blocks are complete. Commit commands include exact file paths.

### Out-of-scope confirmation

- Handler integration (slice 3): not touched ✓
- Watches re-enqueueing handlers on KapeSkill changes (slice 4): not touched ✓
- `resolvedDependencies` struct (slice 3 cross-slice contract): not introduced ✓
