# GitHub Sync — Slice 1 Spec: Parser + Dry-Run CLI

## Goal

Implement a `go run ./tools/sync-github` CLI that parses the KAPE roadmap markdown files
into a structured in-memory model and prints a dry-run plan — phases, milestones, and slices
that *would* be synced to GitHub — without making any GitHub API calls.
The `make sync-github` Make target invokes this tool.

---

## Architecture Decomposition (all slices)

| Slice | Deliverable | Observable value |
|---|---|---|
| 1 (this) | Parser + dry-run CLI (`--dry-run` default) | Verifies parsing before touching GitHub |
| 2 | Milestone sync | Phases → GitHub Milestones, idempotent create/update |
| 3 | Issue sync + stale handling | Slices → GitHub Issues; close removed slices with `roadmap-removed` label |

---

## Files to Create / Modify

| Action | Path | Responsibility |
|---|---|---|
| **Create** | `tools/sync-github/main.go` | CLI entry point: parse flags, call parser, emit plan |
| **Create** | `tools/sync-github/parser/parser.go` | `ParseRoadmap()` — reads phases.md + phase READMEs + plans/ |
| **Create** | `tools/sync-github/parser/parser_test.go` | Table-driven unit tests using testdata fixtures |
| **Create** | `tools/sync-github/parser/testdata/phases.md` | Minimal fixture: 2–3 rows |
| **Create** | `tools/sync-github/parser/testdata/phases/01-example/README.md` | Minimal fixture phase README |
| **Create** | `tools/sync-github/parser/testdata/phases/01-example/plans/slice-1-foo.md` | Minimal fixture plan file |
| **Create** | `tools/sync-github/model/model.go` | `Phase`, `Slice`, `RoadmapModel` structs |
| **Modify** | `Makefile` | Add `sync-github` target |

---

## Data Structures

```go
// tools/sync-github/model/model.go

package model

// PhaseStatus mirrors the values used in phases.md
type PhaseStatus string

const (
    StatusDone       PhaseStatus = "done"
    StatusInProgress PhaseStatus = "in_progress"
    StatusPending    PhaseStatus = "pending"
)

// Slice represents one plan file inside a phase's plans/ directory.
type Slice struct {
    // Number is the ordinal extracted from the filename, e.g. "1" from "slice-1-kapeskill-crd.md"
    Number int
    // Slug is the full filename stem, e.g. "slice-1-kapeskill-crd"
    Slug string
    // Title is derived from the H1 heading inside the plan file, falling back to Slug if absent.
    Title string
    // PhaseNumber is the parent phase number.
    PhaseNumber int
    // PlanFile is the absolute path to the source markdown file.
    PlanFile string
}

// Phase represents one row in docs/roadmap/phases.md plus its parsed slices.
type Phase struct {
    // Number is the phase ordinal (1–10).
    Number int
    // Name is the human-readable name from the phases.md table, e.g. "Full Operator".
    Name string
    // Status is the current status from phases.md.
    Status PhaseStatus
    // Milestone is the milestone label from phases.md, e.g. "M2". Empty string for "—".
    Milestone string
    // ReadmeFile is the absolute path to the phase README.md.
    ReadmeFile string
    // Slices holds the ordered list of plan files found in plans/.
    Slices []Slice
}

// RoadmapModel is the parsed representation of the entire roadmap.
type RoadmapModel struct {
    // Phases in ordinal order.
    Phases []Phase
    // RoadmapFile is the absolute path to docs/roadmap/phases.md.
    RoadmapFile string
    // ParsedAt is the UTC timestamp of the parse run (RFC3339).
    ParsedAt string
}
```

---

## Parsing Logic

### Input layout (authoritative)

```
docs/roadmap/phases.md                     # master table
docs/roadmap/phases/<NN>-<slug>/           # per-phase directory
    README.md                              # phase overview
    plans/                                 # optional; may be absent
        slice-<N>-<slug>.md               # one per implementation slice
```

### `ParseRoadmap(roadmapRoot string) (*RoadmapModel, error)`

`roadmapRoot` is the absolute path to the repo root (e.g. `/home/tony/projects/kape-io`).
The function walks `docs/roadmap/` relative to that root.

#### Step A — Parse phases.md

1. Read `{roadmapRoot}/docs/roadmap/phases.md`.
2. Find the markdown table (lines starting with `|`).
3. Skip the header row (row 0) and separator row (row 1, contains `---`).
4. For each data row, split on `|` and trim whitespace:
   - Column 0: phase number (strip leading/trailing spaces; parse as int)
   - Column 1: phase name (strip markdown formatting if any)
   - Column 2: status (one of `done`, `in_progress`, `pending`)
   - Column 3: milestone label (normalise `—` / `–` / `-` to `""`)
   - Column 4: ignored (specs refs)
   - Column 5: file link — extract directory path from markdown link text like `[phases/06-full-operator.md](phases/06-full-operator.md)` using a regex `\[([^\]]+)\]\(([^)]+)\)`, take capture group 2, strip the `.md` suffix to get the **directory path** (e.g. `phases/06-full-operator`).
5. Build a `Phase` struct for each row.

> **Edge case:** Rows where the phase number cell contains only whitespace or dashes are skipped.

#### Step B — Resolve phase directories

For each `Phase` parsed above:
1. Construct `phaseDir = filepath.Join(roadmapRoot, "docs/roadmap", phaseDirPath)` where `phaseDirPath` is the value extracted in Step A column 5.
2. Check if `phaseDir` is a directory (not a file). If the path ends in `.md` (old flat layout), check for a sibling directory with the `.md` stripped.
3. Set `Phase.ReadmeFile = filepath.Join(phaseDir, "README.md")`.

#### Step C — Parse slices from plans/

For each `Phase`:
1. `plansDir = filepath.Join(phaseDir, "plans")`. If it does not exist, `Phase.Slices` is empty — that is valid (phases 1–4 have no plans/).
2. `os.ReadDir(plansDir)` — collect all `.md` files matching `slice-<N>-<slug>.md` using regex `^slice-(\d+)-(.+)\.md$`.
3. Sort by the numeric capture group (ascending).
4. For each matching file:
   a. Parse `Number` from capture group 1 (int).
   b. Parse `Slug` from the full filename stem (without `.md`).
   c. Read the file and extract the first H1 heading (`# ` prefix, first match). If no H1 found, `Title = Slug`.
   d. Set `PlanFile` to the absolute path.
5. Append to `Phase.Slices`.

#### Step D — Return model

Return `&RoadmapModel{Phases: phases, RoadmapFile: phasesFilePath, ParsedAt: time.Now().UTC().Format(time.RFC3339)}`.

---

## CLI Interface

### Entry point: `tools/sync-github/main.go`

```
Usage: sync-github [flags]

Flags:
  --root     string   Repo root path (default: auto-detected via git rev-parse --show-toplevel)
  --dry-run  bool     Print plan without making any GitHub API calls (default: true)
  --format   string   Output format: "table" or "json" (default: "table")
```

### Dry-run table output (example)

```
KAPE Roadmap → GitHub Sync Plan
Roadmap parsed at: 2026-05-16T10:00:00Z

PHASES (→ Milestones)
  Phase 1  CRDs + CEL Validation    done        [no milestone]
  Phase 5  AlertManager Adapter     done        M1
  Phase 6  Full Operator            in_progress  M2
    └── slice-1  KapeSkill CRD + KapeSkillReconciler Implementation Plan
    └── slice-2  KapeSchema Finalizer + Kubernetes Events
    ...
  Phase 7  Full Runtime             pending     M3
  ...

Total: 10 phases, 4 with milestones, 7 slices found.
```

### JSON output (--format=json)

Serialise the full `RoadmapModel` as indented JSON. Field names match struct field names in camelCase via `json` tags.

### Auto-detect repo root

```go
func detectRepoRoot() (string, error) {
    out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
    if err != nil {
        return "", fmt.Errorf("not inside a git repository: %w", err)
    }
    return strings.TrimSpace(string(out)), nil
}
```

### Make target

Add to `Makefile`:

```makefile
##@ GitHub Sync

.PHONY: sync-github
sync-github: ## Sync roadmap to GitHub Milestones and Issues (dry-run by default)
	go run ./tools/sync-github --root=$(shell git rev-parse --show-toplevel) --dry-run=true
```

---

## Label Strategy (defined here, implemented in Slice 3)

Define the label constants in `tools/sync-github/model/model.go` so Slices 2 and 3 share them:

```go
const (
    // LabelRoadmapSync is applied to all GitHub Issues managed by this tool.
    // Never apply it manually — it is the ownership marker.
    LabelRoadmapSync = "roadmap-sync"

    // LabelRoadmapRemoved is applied (alongside closing) when a managed issue's
    // slice is no longer present in the roadmap.
    LabelRoadmapRemoved = "roadmap-removed"
)
```

---

## Idempotency Design (defined here, implemented in Slice 3)

Matching strategy for GitHub Issues → roadmap slices:

**Primary key**: `{phase_number}/{slice_slug}` encoded as a GitHub Issue title prefix:

```
[P6/slice-1-kapeskill-crd] KapeSkill CRD + KapeSkillReconciler Implementation Plan
```

Rationale: The title prefix is stable across runs. The `roadmap-sync` label narrows the search space; the prefix provides exact matching within that set. No external mapping file is needed.

Matching algorithm (Slice 3):
1. List all open + closed GitHub Issues with label `roadmap-sync` via `GET /repos/{owner}/{repo}/issues?labels=roadmap-sync&state=all&per_page=100` (paginate).
2. For each issue, extract the prefix `[P{N}/{slug}]` from the title using regex `^\[P(\d+)/(slice-[^\]]+)\]`.
3. Build a map `key → GitHubIssue` where `key = "{N}/{slug}"`.
4. For each `Slice` in the current roadmap model: look up by key.
   - Found + open → update title/body if changed.
   - Found + closed → reopen + update.
   - Not found → create.
5. For each managed issue whose key is NOT in the current roadmap: close + apply `roadmap-removed` label.

---

## Error Handling

| Boundary | Handling |
|---|---|
| `docs/roadmap/phases.md` missing | Fatal: `log.Fatalf("phases.md not found at %s", path)` |
| Phase directory missing | Warning logged; phase included with `Slices: []` (graceful degradation) |
| `plans/` directory absent | Silently skip; valid for early phases |
| Plan file parse error (bad H1 extraction) | Warning logged; `Title = Slug` fallback used |
| Non-`.md` files in `plans/` | Silently skip |
| Files in `plans/` not matching `slice-N-slug.md` | Silently skip (e.g. `README.md` in plans/) |
| `git rev-parse` fails for auto-detect | Fatal with clear message |

---

## Acceptance Criteria

1. `go run ./tools/sync-github --dry-run=true` exits 0 and prints a table listing all 10 phases.
2. Each of the 10 phases shows the correct name and status as found in `docs/roadmap/phases.md`.
3. Phase 6 shows all 7 slices (slice-1 through slice-7) under its entry.
4. Phases 1–4 show 0 slices (no `plans/` directory).
5. Phase 5 shows 0 slices (has `README.md` but no `plans/`).
6. `--format=json` produces valid JSON that deserialises into `RoadmapModel` without error.
7. `make sync-github` invokes the tool and exits 0.
8. `ParseRoadmap()` unit tests pass for: (a) normal 3-phase fixture with slices, (b) phase with no `plans/` directory, (c) plan file with no H1 heading (falls back to slug).
9. Running the tool twice produces identical output (fully deterministic, no side effects).
10. No GitHub API calls are made in any code path when `--dry-run=true` (verified by absence of `gh` or `net/http` calls with GitHub base URL in Slice 1 code).

---

## Out of Scope for Slice 1

- Any GitHub API calls (milestones, issues)
- Authentication / `GITHUB_TOKEN`
- The `--dry-run=false` flag path (exits with "not yet implemented" error in Slice 1)
- Label creation on GitHub
- Stale issue detection and closing
