# Docs Restructure Design — specs/ and roadmap/

**Date:** 2026-04-19
**Status:** Approved

---

## Problem

The root `specs/` directory mixes two categories of documents that have different lifecycles:

- **Design specs** (0001–0013): frozen design decisions from individual sessions. These record intent and are referenced by the roadmap.
- **Roadmap** (0012-v1-roadmap): a living build sequence that changes every time a new session produces a new spec.

Keeping the roadmap in `specs/` creates a category error. Specs record immutable decisions; the roadmap is planning state that evolves each session. Additionally, `docs/specs/` and `docs/plans/` already exist as empty directories, indicating intent to consolidate documentation under `docs/`.

---

## Design

### Folder Structure

```
docs/
  specs/
    plan.md                     ← moved from specs/plan.md
    0001-rfc/
    0002-crds-design/
    0003-q&a/
    0004-kape-handler/
    0005-kape-operator/
    0006-events-broker-design/
    0007-security-layer/
    0008-audit-db/
    0009-dashboard-ui/
    0010-CEL-rules/
    0011-repo-structure/
    0012-v1-roadmap/            ← archived baseline, not the live roadmap
    0013-kape-skill-crd/
  roadmap/
    phases.md                   ← status index (stays small forever)
    phases/
      01-crds-cel.md
      02-minimal-operator.md
      03-task-service.md
      04-minimal-runtime.md
      05-alertmanager-adapter.md
      06-full-operator.md
      07-full-runtime.md
      08-audit-security.md
      09-dashboard.md
      10-helm-polish.md
  superpowers/                  ← unchanged
  plans/                        ← unchanged (superpowers-generated plans)
```

The root `specs/` directory is removed after migration.

---

## File Content Contracts

### `docs/specs/plan.md`

Moved verbatim from `specs/plan.md`. No content changes — it is the session discussion guide and already self-contained.

### `docs/specs/0001-*/` … `0013-*/`

Moved verbatim from `specs/0001-*/` … `0013-*/`. Content is frozen.

`specs/0012-v1-roadmap/` is retained as an archived baseline. It is not the live roadmap — `docs/roadmap/` is. Its README carries a note pointing to `docs/roadmap/`.

### `docs/roadmap/phases.md`

Status index only. Contains one table and nothing else:

```markdown
# KAPE Build Sequence

| Phase | Name | Status | Milestone | Specs | File |
|---|---|---|---|---|---|
| 1  | CRDs + CEL Validation   | done    | —  | 0002, 0010       | [phases/01-crds-cel.md](phases/01-crds-cel.md) |
| 2  | Minimal Operator        | done    | —  | 0002, 0005       | [phases/02-minimal-operator.md](phases/02-minimal-operator.md) |
| 3  | Task Service            | done    | —  | 0008, 0009       | [phases/03-task-service.md](phases/03-task-service.md) |
| 4  | Minimal Runtime         | done    | —  | 0001, 0004       | [phases/04-minimal-runtime.md](phases/04-minimal-runtime.md) |
| 5  | AlertManager Adapter    | done    | M1 | 0006             | [phases/05-alertmanager-adapter.md](phases/05-alertmanager-adapter.md) |
| 6  | Full Operator           | pending | M2 | 0002, 0005, 0013 | [phases/06-full-operator.md](phases/06-full-operator.md) |
| 7  | Full Runtime            | pending | M3 | 0004, 0006, 0013 | [phases/07-full-runtime.md](phases/07-full-runtime.md) |
| 8  | K8s Audit + Security    | pending | M4 | 0006, 0007       | [phases/08-audit-security.md](phases/08-audit-security.md) |
| 9  | Dashboard               | pending | M5 | 0009, 0008       | [phases/09-dashboard.md](phases/09-dashboard.md) |
| 10 | Helm + Examples + Polish| pending | M6 | 0011             | [phases/10-helm-polish.md](phases/10-helm-polish.md) |
```

Status values: `done` | `in-progress` | `pending`.

### `docs/roadmap/phases/XX-name.md`

Each phase file carries full detail: goal, work items, acceptance criteria, key files. Header format:

```markdown
# Phase N — Name

**Status:** done | in-progress | pending
**Milestone:** MN | —
**Specs:** 0002, 0010
**Modified by:** 0012 (created), 0013 (KapeSkill + KapeProxy added)
```

The `Modified by:` line is updated in-place each time a hydration cycle changes the phase. Git history records the diff.

---

## Hydration Workflow

Each new session (hydration cycle):

1. Creates a new spec directory `docs/specs/NNNN-topic/`
2. Updates `docs/specs/plan.md` session index (mark session done, add next session)
3. For each affected phase: updates `docs/roadmap/phases/XX-name.md` (adds work items, updates acceptance criteria, updates `Modified by:` header)
4. Updates `docs/roadmap/phases.md` status table (new phase rows or updated `Specs` column)

No separate hydration changelog files are needed — the `Modified by:` header in each phase file plus git history provide full traceability.

---

## Migration Steps

1. Move `specs/0001-*/` … `specs/0013-*/` → `docs/specs/`
2. Move `specs/plan.md` → `docs/specs/plan.md`
3. Add deprecation note to `specs/0012-v1-roadmap/README.md` pointing to `docs/roadmap/`
4. Create `docs/roadmap/phases.md` with status table
5. Create `docs/roadmap/phases/01-crds-cel.md` … `10-helm-polish.md` — content sourced from `specs/0012-v1-roadmap/README.md` phase sections, split one file per phase; phase 6 and 7 updated with 0013 additions
6. Delete root `specs/` directory
7. Commit

---

## What Does Not Change

- `docs/superpowers/` — unchanged
- `docs/plans/` — unchanged
- All other project files and directories
