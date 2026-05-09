# Phase 6 Slice 2 — KapeSchema Event Recording Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Emit `DeletionBlocked` (Warning) and `SchemaValid` (Normal) Kubernetes events from the KapeSchema reconciler, which currently only writes Conditions and records no events.

**Architecture:** Add a `record.EventRecorder` field to `SchemaReconciler` in `operator/controller/reconcile/schema.go`, wire it in `operator/controller/schema.go` via `mgr.GetEventRecorderFor`, and call `recorder.Event(schema, ...)` at the two existing decision points: successful validation (SchemaValid) and deletion-blocked-by-handler-reference (DeletionBlocked). Extend `schema_test.go` with a fake recorder to assert both events are emitted. No new files are created.

**Tech Stack:** Go 1.23+, controller-runtime v0.19+, `k8s.io/client-go/tools/record`, `k8s.io/api/core/v1` (EventTypeWarning / EventTypeNormal), testify assertions.

---

## File Map

| File | Change |
|---|---|
| `operator/controller/reconcile/schema.go` | Add `recorder record.EventRecorder` field; add event emission in `Reconcile` (step 5) and `handleDeletion` |
| `operator/controller/schema.go` | Pass `mgr.GetEventRecorderFor("kapeschema-controller")` to `NewSchemaReconciler` |
| `operator/cmd/main.go` | Update `NewSchemaReconciler` call to pass the recorder (after schema.go wiring) |
| `operator/controller/reconcile/schema_test.go` | Add `record.NewFakeRecorder`, extend two existing tests and add two new event-assertion tests |

---

## Task 1: Add event recorder field to SchemaReconciler

**Files:**
- Modify: `operator/controller/reconcile/schema.go`

- [ ] **Step 1.1: Read the current file before editing**

Read `/home/tony/projects/kape-io/operator/controller/reconcile/schema.go` in full to confirm the exact import block and struct definition before making changes.

- [ ] **Step 1.2: Add `record` import and recorder field**

Replace the import block and struct in `operator/controller/reconcile/schema.go`:

```go
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)
```

Replace the struct and constructor:

```go
// SchemaReconciler performs the full reconcile logic for KapeSchema.
type SchemaReconciler struct {
	schemas  ports.SchemaRepository
	recorder record.EventRecorder
}

// NewSchemaReconciler creates a SchemaReconciler.
func NewSchemaReconciler(schemas ports.SchemaRepository, recorder record.EventRecorder) *SchemaReconciler {
	return &SchemaReconciler{schemas: schemas, recorder: recorder}
}
```

- [ ] **Step 1.3: Verify the file compiles (no event calls yet)**

```bash
cd /home/tony/projects/kape-io/operator && go build ./...
```

Expected: compile error mentioning `NewSchemaReconciler` call in `main.go` (wrong number of arguments) — this is expected, we have not updated the callers yet. Any other error means Step 1.2 has a mistake to fix.

---

## Task 2: Add SchemaValid event emission in Reconcile

**Files:**
- Modify: `operator/controller/reconcile/schema.go` (step 5 of Reconcile)

The `SchemaValid` (Normal) event must be emitted immediately after `UpdateStatus` succeeds at the end of the happy path (current lines 70–79). The event fires once per successful reconcile — controller-runtime deduplication prevents log spam.

- [ ] **Step 2.1: Emit SchemaValid event in Reconcile**

In `operator/controller/reconcile/schema.go`, replace the closing lines of `Reconcile` (the block that sets Ready=True and calls UpdateStatus) with:

```go
	// 5. Set Ready=True
	schema.Status.Conditions = setCondition(schema.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Valid",
		Message: "JSON Schema validated successfully",
	})
	if err := r.schemas.UpdateStatus(ctx, schema); err != nil {
		return ctrl.Result{}, err
	}
	r.recorder.Event(schema, corev1.EventTypeNormal, "SchemaValid", "JSON Schema validated successfully")
	return ctrl.Result{}, nil
```

- [ ] **Step 2.2: Verify file still has correct structure**

```bash
cd /home/tony/projects/kape-io/operator && go vet ./controller/reconcile/...
```

Expected: only the same arity-mismatch error from `main.go` — nothing from `reconcile/`.

---

## Task 3: Add DeletionBlocked event emission in handleDeletion

**Files:**
- Modify: `operator/controller/reconcile/schema.go` (`handleDeletion` function)

The `DeletionBlocked` (Warning) event must fire when `handleDeletion` discovers at least one referencing handler and blocks deletion.

- [ ] **Step 3.1: Emit DeletionBlocked event in handleDeletion**

In `operator/controller/reconcile/schema.go`, replace the `len(handlers) > 0` branch in `handleDeletion` with:

```go
	if len(handlers) > 0 {
		names := make([]string, 0, len(handlers))
		for _, h := range handlers {
			names = append(names, h.Name)
		}
		msg := fmt.Sprintf("Cannot delete: referenced by handlers: [%s]", strings.Join(names, ", "))
		schema.Status.Conditions = setCondition(schema.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ReferencedByHandlers",
			Message: msg,
		})
		_ = r.schemas.UpdateStatus(ctx, schema)
		r.recorder.Event(schema, corev1.EventTypeWarning, "DeletionBlocked", msg)
		return ctrl.Result{}, nil // blocked — no requeue; re-triggered on handler deletion
	}
```

- [ ] **Step 3.2: Verify reconcile package compiles cleanly**

```bash
cd /home/tony/projects/kape-io/operator && go vet ./controller/reconcile/...
```

Expected: zero errors from `reconcile/` package.

---

## Task 4: Wire EventRecorder in schema controller wiring and main.go

**Files:**
- Modify: `operator/controller/schema.go`
- Modify: `operator/cmd/main.go`

`mgr.GetEventRecorderFor` is the standard controller-runtime way to obtain a `record.EventRecorder`. The component name appears in the `reportingComponent` field of emitted events.

- [ ] **Step 4.1: Read schema.go before editing**

Read `/home/tony/projects/kape-io/operator/controller/schema.go` to confirm the current `SetupSchemaReconciler` signature.

- [ ] **Step 4.2: Update SetupSchemaReconciler to accept and pass manager**

The function already receives `mgr manager.Manager`. Use it to get the recorder. No signature change needed — `NewSchemaReconciler` is called inside `SetupSchemaReconciler`, so we only need to pass the recorder there.

Replace `SetupSchemaReconciler` in `operator/controller/schema.go`:

```go
// SetupSchemaReconciler registers the KapeSchema reconciler with the controller manager.
func SetupSchemaReconciler(mgr manager.Manager, inner *reconcile.SchemaReconciler, maxConcurrent int) error {
	r := NewKapeSchemaReconciler(inner)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KapeSchema{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrent}).
		Complete(r)
}
```

The recorder is obtained in `main.go` and passed to `NewSchemaReconciler` before `SetupSchemaReconciler` is called. `schema.go` itself does not need to change — `inner` is already fully constructed before it arrives here.

- [ ] **Step 4.3: Update main.go to pass recorder to NewSchemaReconciler**

In `operator/cmd/main.go`, find the block:

```go
	// KapeSchemaReconciler
	schemaRec := reconcilehandler.NewSchemaReconciler(schemaRepo)
```

Replace with:

```go
	// KapeSchemaReconciler
	schemaRecorder := mgr.GetEventRecorderFor("kapeschema-controller")
	schemaRec := reconcilehandler.NewSchemaReconciler(schemaRepo, schemaRecorder)
```

- [ ] **Step 4.4: Verify the whole operator compiles**

```bash
cd /home/tony/projects/kape-io/operator && go build ./...
```

Expected: zero errors.

- [ ] **Step 4.5: Run existing schema tests to confirm no regression**

```bash
cd /home/tony/projects/kape-io/operator && go test ./controller/reconcile/ -run TestSchemaReconciler -v
```

Expected: all four existing `TestSchemaReconciler_*` tests FAIL with a nil-pointer panic or similar — because they construct `NewSchemaReconciler(schemaRepo)` with the old one-argument form and we changed the signature. This is expected — we fix the tests in Task 5.

---

## Task 5: Update existing tests to pass a fake recorder

**Files:**
- Modify: `operator/controller/reconcile/schema_test.go`

`record.NewFakeRecorder(n)` creates an in-memory recorder whose events are written to a buffered channel `Events chan string`. Each event string has the format `"<type> <reason> <message>"`. We pass a buffer large enough that no test blocks on write (buffer size 10 is sufficient for all scenarios).

- [ ] **Step 5.1: Read the test file before editing**

Read `/home/tony/projects/kape-io/operator/controller/reconcile/schema_test.go` in full.

- [ ] **Step 5.2: Add record import and fakeRecorder helper**

Add `"k8s.io/client-go/tools/record"` to the import block.

Add a helper at the top of the test file (after `newSchemaScheme`):

```go
func newFakeRecorder() *record.FakeRecorder {
	return record.NewFakeRecorder(10)
}
```

- [ ] **Step 5.3: Update all four existing tests to pass the recorder**

Each test currently calls `reconcile.NewSchemaReconciler(k8sadapters.NewSchemaRepository(c))`. Update every call to:

```go
reconcile.NewSchemaReconciler(k8sadapters.NewSchemaRepository(c), newFakeRecorder())
```

There are four test functions to update:
- `TestSchemaReconciler_ValidSchema_SetsReadyAndHash`
- `TestSchemaReconciler_InvalidSchema_SetsNotReady`
- `TestSchemaReconciler_DeletionBlockedWhenHandlerReferences`
- `TestSchemaReconciler_FinalizerAddedOnCreate`

- [ ] **Step 5.4: Run updated existing tests to confirm they pass**

```bash
cd /home/tony/projects/kape-io/operator && go test ./controller/reconcile/ -run TestSchemaReconciler -v
```

Expected: all four tests PASS. If any fail, read the error and fix before proceeding.

- [ ] **Step 5.5: Commit the recorder wiring**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice2-plan add \
  operator/controller/reconcile/schema.go \
  operator/controller/schema.go \
  operator/cmd/main.go \
  operator/controller/reconcile/schema_test.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice2-plan commit -m "feat(operator): add event recorder to KapeSchema reconciler"
```

---

## Task 6: Write failing tests for SchemaValid and DeletionBlocked events

**Files:**
- Modify: `operator/controller/reconcile/schema_test.go`

`record.FakeRecorder.Events` is a `chan string`. Each emitted event lands as a string `"<EventType> <Reason> <message>"` — e.g. `"Normal SchemaValid JSON Schema validated successfully"`. We drain the channel with a non-blocking select to avoid test hangs.

- [ ] **Step 6.0: Add drainEvents helper to the test file**

Add this helper immediately after `newFakeRecorder` in `operator/controller/reconcile/schema_test.go`:

```go
func drainEvents(rec *record.FakeRecorder) []string {
	var events []string
	for {
		select {
		case e := <-rec.Events:
			events = append(events, e)
		default:
			return events
		}
	}
}
```

- [ ] **Step 6.1: Write the failing test for SchemaValid event**

Add to `operator/controller/reconcile/schema_test.go`:

```go
func TestSchemaReconciler_ValidSchema_EmitsSchemaValidEvent(t *testing.T) {
	schema := validSchema()
	c := fake.NewClientBuilder().WithScheme(newSchemaScheme()).WithObjects(schema).WithStatusSubresource(schema).Build()
	rec := newFakeRecorder()
	r := reconcile.NewSchemaReconciler(k8sadapters.NewSchemaRepository(c), rec)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})

	require.NoError(t, err)
	events := drainEvents(rec)
	assert.Contains(t, events, "Normal SchemaValid JSON Schema validated successfully")
}
```

- [ ] **Step 6.2: Run the test to confirm it FAILS**

```bash
cd /home/tony/projects/kape-io/operator && go test ./controller/reconcile/ -run TestSchemaReconciler_ValidSchema_EmitsSchemaValidEvent -v
```

Expected: FAIL — `assert.Contains` fails because no event was emitted yet (this is the TDD red phase confirming the test is wired correctly). If it passes, the event emission from Task 2 is already active — skip to Step 6.3.

- [ ] **Step 6.3: Write the failing test for DeletionBlocked event**

Add to `operator/controller/reconcile/schema_test.go`:

```go
func TestSchemaReconciler_DeletionBlockedEmitsDeletionBlockedEvent(t *testing.T) {
	now := metav1.Now()
	schema := validSchema()
	schema.DeletionTimestamp = &now
	schema.Finalizers = []string{"kape.io/schema-protection"}

	handler := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-handler",
			Namespace: "kape-system",
			Labels:    map[string]string{"kape.io/schema-ref": "my-schema"},
		},
	}

	c := fake.NewClientBuilder().WithScheme(newSchemaScheme()).WithObjects(schema, handler).WithStatusSubresource(schema).Build()
	rec := newFakeRecorder()
	r := reconcile.NewSchemaReconciler(k8sadapters.NewSchemaRepository(c), rec)

	_, err := r.Reconcile(context.Background(), types.NamespacedName{Name: "my-schema", Namespace: "kape-system"})

	require.NoError(t, err)
	events := drainEvents(rec)
	// At least one event must be a Warning DeletionBlocked event
	found := false
	for _, e := range events {
		if strings.HasPrefix(e, "Warning DeletionBlocked") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected a Warning DeletionBlocked event, got: %v", events)
}
```

Note: the `strings` package import must be present in the test file. Add `"strings"` to the import block if not already there.

- [ ] **Step 6.4: Run the test to confirm it FAILS**

```bash
cd /home/tony/projects/kape-io/operator && go test ./controller/reconcile/ -run TestSchemaReconciler_DeletionBlockedEmitsDeletionBlockedEvent -v
```

Expected: FAIL — no `Warning DeletionBlocked` event in channel yet.

---

## Task 7: Run all schema tests to confirm both new tests now PASS

After Tasks 2 and 3 already added the event calls, the tests from Task 6 should now pass without any further code changes.

- [ ] **Step 7.1: Run all schema tests**

```bash
cd /home/tony/projects/kape-io/operator && go test ./controller/reconcile/ -run TestSchemaReconciler -v
```

Expected output (all six tests pass):

```
--- PASS: TestSchemaReconciler_ValidSchema_SetsReadyAndHash
--- PASS: TestSchemaReconciler_InvalidSchema_SetsNotReady
--- PASS: TestSchemaReconciler_DeletionBlockedWhenHandlerReferences
--- PASS: TestSchemaReconciler_FinalizerAddedOnCreate
--- PASS: TestSchemaReconciler_ValidSchema_EmitsSchemaValidEvent
--- PASS: TestSchemaReconciler_DeletionBlockedEmitsDeletionBlockedEvent
PASS
```

If any test fails, diagnose the error message and fix before continuing.

- [ ] **Step 7.2: Run all operator tests to check for regressions**

```bash
cd /home/tony/projects/kape-io/operator && go test ./... 2>&1
```

Expected: all tests pass. Any failures outside `controller/reconcile/` are regressions that must be fixed before proceeding.

- [ ] **Step 7.3: Commit the event tests**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice2-plan add \
  operator/controller/reconcile/schema_test.go
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice2-plan commit -m "test(operator): assert SchemaValid and DeletionBlocked events in KapeSchema reconciler"
```

---

## Task 8: Snyk Code scan on operator/

Per global instructions and CLAUDE.md, run a Snyk Code scan on any new or modified first-party Go code before raising a PR.

- [ ] **Step 8.1: Run Snyk Code scan**

Call `mcp__Snyk__snyk_code_scan` with:
- `path`: `/home/tony/projects/kape-io/operator`

- [ ] **Step 8.2: Review results**

If any issues are flagged in files you modified (`schema.go`, `schema_test.go`, `main.go`):
1. Read the issue description in the scan results.
2. Apply the minimal fix.
3. Re-run the scan.
4. Repeat until the scan reports no issues in modified files.

If the scan returns no issues in modified files, proceed.

---

## Task 9: Run SBOM scans for all three modules

Per `kape-io/CLAUDE.md`, before raising any PR, run `snyk_sbom_scan` on all three Go modules.

- [ ] **Step 9.1: Run SBOM scan on adapters/**

Call `mcp__Snyk__snyk_sbom_scan` with:
- `path`: `/home/tony/projects/kape-io/adapters`
- `format`: `cyclonedx1.4+json`

Record: component count, flagged count.

- [ ] **Step 9.2: Run SBOM scan on operator/**

Call `mcp__Snyk__snyk_sbom_scan` with:
- `path`: `/home/tony/projects/kape-io/operator`
- `format`: `cyclonedx1.4+json`

Record: component count, flagged count.

- [ ] **Step 9.3: Run SBOM scan on task-service/**

Call `mcp__Snyk__snyk_sbom_scan` with:
- `path`: `/home/tony/projects/kape-io/task-service`
- `format`: `cyclonedx1.4+json`

Record: component count, flagged count.

---

## Task 10: Push branch and open PR with SBOM summary comment

- [ ] **Step 10.1: Push the branch**

```bash
git -C /home/tony/projects/kape-io/.worktrees/docs-phase6-slice2-plan push -u origin docs/phase6-slice2-plan
```

- [ ] **Step 10.2: Open PR**

```bash
gh pr create \
  --repo dzungtr/kape \
  --base main \
  --head docs/phase6-slice2-plan \
  --title "feat(operator): phase 6 slice 2 — KapeSchema event recording" \
  --body "$(cat <<'EOF'
## Summary

- Adds `record.EventRecorder` to `SchemaReconciler` (operator/controller/reconcile/schema.go)
- Emits `SchemaValid` (Normal) event on successful JSON Schema validation
- Emits `DeletionBlocked` (Warning) event when deletion is blocked by a referencing KapeHandler
- Wires the recorder in `operator/controller/schema.go` via `mgr.GetEventRecorderFor`
- Extends `schema_test.go` with two new event-assertion tests using `record.FakeRecorder`

## Acceptance Criteria (from Phase 6 Slice 2 spec)

- All existing KapeSchema reconciler tests pass (no regression)
- New test `TestSchemaReconciler_ValidSchema_EmitsSchemaValidEvent` passes
- New test `TestSchemaReconciler_DeletionBlockedEmitsDeletionBlockedEvent` passes

## Test plan

- [ ] `go test ./controller/reconcile/ -run TestSchemaReconciler -v` — all 6 pass
- [ ] `go test ./...` — no regressions
- [ ] Snyk Code scan clean on modified files
- [ ] SBOM summary posted as comment (see below)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 10.3: Post SBOM summary comment**

After the PR is created, run:

```bash
gh pr comment "$(gh pr view --json url --jq '.url' --repo dzungtr/kape)" \
  --repo dzungtr/kape \
  --body "$(cat <<'EOF'
## SBOM Summary

| Module | Components | Flagged |
|---|---|---|
| adapters | <count from step 9.1> | <flagged from step 9.1 or "none"> |
| operator | <count from step 9.2> | <flagged from step 9.2 or "none"> |
| task-service | <count from step 9.3> | <flagged from step 9.3 or "none"> |

Generated via Snyk CycloneDX 1.4 — <current UTC timestamp e.g. 2026-05-09T10:00:00Z>
EOF
)"
```

Replace `<count>` and `<flagged>` placeholders with the actual values recorded in Task 9. Compute the current UTC timestamp and insert it literally before posting.

---

## Acceptance Criteria

- [ ] `go test ./controller/reconcile/ -run TestSchemaReconciler -v` reports 6 passing tests
- [ ] `go test ./...` in `operator/` reports no failures
- [ ] `SchemaValid` (Normal) event is emitted after successful schema validation
- [ ] `DeletionBlocked` (Warning) event is emitted when deletion is blocked by handler reference
- [ ] Snyk Code scan reports no issues in modified files
- [ ] PR is open with SBOM summary comment
