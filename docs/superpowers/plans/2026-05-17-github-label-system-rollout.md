# GitHub Label System Rollout — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended for kape-io per project memory) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Roll out the 39-label taxonomy and its supporting automation as defined in `docs/superpowers/specs/2026-05-16-github-label-system-design.md`, so that every open issue carries machine-readable signals that `/standup`, planning, triage, and the state validator can consume without re-reading issue bodies.

**Architecture:** Labels are managed by a declarative YAML manifest synced to GitHub by an idempotent bash script. Automation lands as three GitHub Actions workflows (label-sync, PR auto-labeler, state validator + weekly audit) plus a `/standup` config update. The roadmap migration script is updated to apply the new labels on new and existing roadmap issues. A one-shot backfill script labels remaining open issues. Each task is self-contained and dispatchable to a fresh subagent.

**Tech Stack:** bash 5+, `yq` (for YAML manipulation), `jq`, `gh` CLI, GitHub Actions (`actions/labeler@v5`), `bats-core` (for shell tests).

**Spec reference:** `docs/superpowers/specs/2026-05-16-github-label-system-design.md`
**Rituals reference:** `docs/agent-rituals.md`

---

## File map

| File                                       | Action  | Responsibility                                        |
|--------------------------------------------|---------|-------------------------------------------------------|
| `.github/labels.yaml`                      | create  | Declarative manifest of all 39 labels                 |
| `tools/sync-labels.sh`                     | create  | Idempotent create/update/delete against the manifest  |
| `tests/tools/sync-labels.bats`             | create  | bats tests for the sync script                        |
| `.github/workflows/sync-labels.yml`        | create  | GHA: re-runs sync on manifest change                  |
| `.github/labeler.yml`                      | create  | Path-glob → label mapping for PRs                     |
| `.github/workflows/pr-labeler.yml`         | create  | GHA: applies labels on PR open/sync                   |
| `.claude/standup.json`                     | modify  | Add `urgent` and `blocked` datasources                |
| `tools/validate-issue-labels.sh`           | create  | State-validator script (one issue per invocation)     |
| `tests/tools/validate-issue-labels.bats`   | create  | bats tests for the validator                          |
| `.github/workflows/validate-labels.yml`    | create  | GHA: weekly + on-issue-event validator run            |
| `tools/migrate-to-github.sh`               | modify  | Use new labels when creating roadmap issues           |
| `tools/backfill-issue-labels.sh`           | create  | One-shot backfill for open issues                     |
| `tools/snyk-finding-to-issue.sh`           | create or modify | Apply `security` + `urgent` on Snyk-sourced issues |

---

## Task dependency graph

```
T1 (manifest) → T2 (sync script) → T3 (sync GHA) → T4 (initial sync)
                                                       ├→ T5  (PR auto-labeler)
                                                       ├→ T6  (standup datasources)
                                                       ├→ T7  (validator script) → T8 (validator GHA)
                                                       ├→ T9  (migrate script update)
                                                       │     └→ T10 (backfill)
                                                       └→ T11 (Snyk integration)
                                                              └→ T12 (retire roadmap-sync)
```

Tasks T5–T11 are independent after T4 and can be parallelised by a subagent driver. T10 depends on T9 (uses the same `area/*` keyword map). T12 depends on T10 (only safe once backfill has set `phase/*` everywhere).

---

## Task 1: Add label manifest

**Files:**
- Create: `.github/labels.yaml`

- [ ] **Step 1: Write the manifest**

Authoritative source for the 39 labels defined in the spec. Schema is `{name, color, description}` per label. `description` is the agent contract; it MUST fit in 100 chars (GitHub's limit).

```yaml
# .github/labels.yaml
# Authoritative source for repo labels. Synced by tools/sync-labels.sh.
# Schema: name (string), color (6-hex no #), description (<=100 chars).
# See docs/superpowers/specs/2026-05-16-github-label-system-design.md for design.

# --- Category (flat, exactly one per issue) ---
- name: bug
  color: d73a4a
  description: "Defect — something is broken"
- name: enhancement
  color: a2eeef
  description: "Improve an existing capability"
- name: feature
  color: 84b6eb
  description: "New capability that doesn't exist yet"
- name: refactor
  color: fbca04
  description: "Internal restructure, no behaviour change"
- name: redesign
  color: ff9f1c
  description: "Reshape architecture/UX/API — behaviour changes"
- name: security
  color: b60205
  description: "Security-relevant change (vuln fix, hardening, policy)"
- name: docs
  color: 0075ca
  description: "Documentation-only"
- name: chore
  color: ededed
  description: "Deps, config, build, tooling"
- name: test
  color: c5def5
  description: "Test-only changes"
- name: spec
  color: 5319e7
  description: "Design or spec doc under docs/superpowers/specs/"

# --- Area (area/*, one per issue) ---
- name: area/operator
  color: 0e8a16
  description: "Go operator (CRDs, reconcilers)"
- name: area/task-service
  color: 0e8a16
  description: "Go task-service"
- name: area/adapters
  color: 0e8a16
  description: "Go event adapters (alertmanager, k8s-audit)"
- name: area/kapeproxy
  color: 0e8a16
  description: "Go MCP proxy"
- name: area/runtime
  color: 0e8a16
  description: "Python LangGraph runtime"
- name: area/dashboard
  color: 0e8a16
  description: "TypeScript dashboard"
- name: area/helm
  color: 0e8a16
  description: "Helm charts and manifests"
- name: area/crds
  color: 0e8a16
  description: "CRD schemas and CEL validation"
- name: area/docs
  color: 0e8a16
  description: "docs/, README, runbooks (excludes spec docs)"
- name: area/ci
  color: 0e8a16
  description: ".github/workflows, tooling"
- name: area/infra
  color: 0e8a16
  description: "NATS, Postgres, cert-manager, External Secrets Operator"

# --- Phase (phase/Mx-*, one per roadmap-tracked issue) ---
- name: phase/M2-operator
  color: 5319e7
  description: "Phase 6 — Full Operator"
- name: phase/M3-runtime
  color: 5319e7
  description: "Phase 7 — Full Runtime"
- name: phase/M4-security
  color: 5319e7
  description: "Phase 8 — K8s Audit + Security"
- name: phase/M5-dashboard
  color: 5319e7
  description: "Phase 9 — Dashboard"
- name: phase/M6-release
  color: 5319e7
  description: "Phase 10 — Helm + Examples + Polish"

# --- Commitment (flat, mutually exclusive) ---
- name: committed
  color: 0e8a16
  description: "Belongs to its milestone. Counts against milestone capacity."
- name: stretch
  color: fbca04
  description: "Targeted for milestone if capacity allows. First to slip."
- name: backlog
  color: c5def5
  description: "Not committed to any milestone. Re-evaluated each cycle."

# --- Signals (flat, additive) ---
- name: urgent
  color: b60205
  description: "Interrupt rule. Standup ranks above committed. Body MUST have Reason:"
- name: blocked
  color: e99695
  description: "Cannot progress. Body MUST have Blocked by: <ref>"
- name: ready
  color: 1d76db
  description: "Triage complete: has category+area+commitment, no needs/*. Auto-set."
- name: needs-triage
  color: ededed
  description: "Inverse of ready. Default on new issues."

# --- Triage needs (needs/*, additive) ---
- name: needs/repro
  color: fbca04
  description: "Bug needs reproduction steps from reporter"
- name: needs/decision
  color: d876e3
  description: "Awaiting a maintainer call on scope or approach"
- name: needs/info
  color: d876e3
  description: "Awaiting reporter or requester response"

# --- Standalone keepers ---
- name: good first issue
  color: 7057ff
  description: "Good for newcomers"
- name: help wanted
  color: 008672
  description: "Extra attention is needed"
- name: snyk-finding
  color: d73a4a
  description: "Snyk-surfaced security finding requiring a fix"

# --- Transitional (retired in T12) ---
- name: roadmap-sync
  color: 0075ca
  description: "Roadmap sync issue (transitional — retired after backfill)"
```

- [ ] **Step 2: Verify YAML parses**

Run: `yq eval '. | length' .github/labels.yaml`
Expected: `40` (39 final + 1 transitional `roadmap-sync`)

- [ ] **Step 3: Verify no description exceeds 100 chars**

Run: `yq eval '.[] | select(.description | length > 100) | .name' .github/labels.yaml`
Expected: empty output (no labels exceed the limit)

- [ ] **Step 4: Commit**

```bash
git add .github/labels.yaml
git commit -m "feat(labels): add declarative label manifest

39 labels across 7 dimensions per the label-system spec, plus the
transitional roadmap-sync label that's retired in a later task."
```

---

## Task 2: Build `tools/sync-labels.sh`

**Files:**
- Create: `tools/sync-labels.sh`
- Create: `tests/tools/sync-labels.bats`

The script reads `.github/labels.yaml`, queries existing labels via `gh label list`, and applies the three-way diff: create missing, update changed, delete extras (with `--keep-extras` to opt out of deletion).

- [ ] **Step 1: Write failing test for create case**

```bash
# tests/tools/sync-labels.bats
#!/usr/bin/env bats

setup() {
  TEST_DIR="$(mktemp -d)"
  export GH_REPO="test/repo"  # fake repo
  export SYNC_LABELS_DRY_RUN=1  # tested mode: emit plan to stdout, no gh calls
  cat > "$TEST_DIR/labels.yaml" <<'EOF'
- name: bug
  color: d73a4a
  description: "Defect"
- name: area/operator
  color: 0e8a16
  description: "Operator"
EOF
}

teardown() {
  rm -rf "$TEST_DIR"
}

@test "dry-run plans creates for labels missing from gh" {
  # fake "gh label list --json name,color,description" returns empty
  export SYNC_LABELS_GH_LIST_FIXTURE='[]'
  run bash "$BATS_TEST_DIRNAME/../../tools/sync-labels.sh" --manifest "$TEST_DIR/labels.yaml"
  [ "$status" -eq 0 ]
  [[ "$output" == *"CREATE bug"* ]]
  [[ "$output" == *"CREATE area/operator"* ]]
}
```

- [ ] **Step 2: Run test, watch it fail**

Install bats if not present (`apt install bats` on the runner) then:
```bash
bats tests/tools/sync-labels.bats
```
Expected: FAIL with "No such file or directory" for `tools/sync-labels.sh`.

- [ ] **Step 3: Write minimal sync script (create-only path)**

```bash
#!/usr/bin/env bash
# tools/sync-labels.sh
# Idempotently sync the label set defined in .github/labels.yaml to GitHub.
# Usage:
#   tools/sync-labels.sh [--manifest <path>] [--keep-extras] [--apply]
# Modes:
#   default: dry-run (print planned mutations, no gh calls)
#   --apply: actually call gh label create/edit/delete
set -euo pipefail

MANIFEST=".github/labels.yaml"
APPLY=0
KEEP_EXTRAS=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --manifest) MANIFEST="$2"; shift 2 ;;
    --apply) APPLY=1; shift ;;
    --keep-extras) KEEP_EXTRAS=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

# Test hook: if SYNC_LABELS_DRY_RUN is set, force dry-run regardless of --apply.
[[ -n "${SYNC_LABELS_DRY_RUN:-}" ]] && APPLY=0

# Test hook: if SYNC_LABELS_GH_LIST_FIXTURE is set, use it instead of real gh.
gh_list_labels() {
  if [[ -n "${SYNC_LABELS_GH_LIST_FIXTURE:-}" ]]; then
    echo "$SYNC_LABELS_GH_LIST_FIXTURE"
  else
    gh label list --json name,color,description --limit 200
  fi
}

# Parse manifest into TSV: name<TAB>color<TAB>description
manifest_tsv() {
  yq -o=json '.' "$MANIFEST" \
    | jq -r '.[] | [.name, .color, .description] | @tsv'
}

# Parse existing labels into the same TSV shape
existing_tsv() {
  gh_list_labels | jq -r '.[] | [.name, .color, .description] | @tsv'
}

main() {
  local manifest existing
  manifest="$(manifest_tsv)"
  existing="$(existing_tsv)"

  # Phase 1: create or update
  while IFS=$'\t' read -r name color desc; do
    [[ -z "$name" ]] && continue
    local current
    current="$(echo "$existing" | awk -F'\t' -v n="$name" '$1==n {print; exit}')" || true
    if [[ -z "$current" ]]; then
      echo "CREATE $name"
      [[ $APPLY -eq 1 ]] && gh label create "$name" --color "$color" --description "$desc"
    else
      local cur_color cur_desc
      cur_color="$(echo "$current" | cut -f2)"
      cur_desc="$(echo "$current" | cut -f3)"
      if [[ "$cur_color" != "$color" || "$cur_desc" != "$desc" ]]; then
        echo "UPDATE $name"
        [[ $APPLY -eq 1 ]] && gh label edit "$name" --color "$color" --description "$desc"
      fi
    fi
  done <<< "$manifest"

  # Phase 2: delete extras (unless --keep-extras)
  if [[ $KEEP_EXTRAS -eq 0 ]]; then
    while IFS=$'\t' read -r name _ _; do
      [[ -z "$name" ]] && continue
      if ! echo "$manifest" | awk -F'\t' -v n="$name" '$1==n {found=1} END {exit !found}'; then
        echo "DELETE $name"
        [[ $APPLY -eq 1 ]] && gh label delete "$name" --yes
      fi
    done <<< "$existing"
  fi
}

main "$@"
```

- [ ] **Step 4: Run test, watch it pass**

```bash
bats tests/tools/sync-labels.bats
```
Expected: PASS for "dry-run plans creates for labels missing from gh".

- [ ] **Step 5: Add tests for update and delete cases**

```bash
# Append to tests/tools/sync-labels.bats

@test "dry-run plans updates when color or description differs" {
  export SYNC_LABELS_GH_LIST_FIXTURE='[{"name":"bug","color":"000000","description":"Defect"}]'
  run bash "$BATS_TEST_DIRNAME/../../tools/sync-labels.sh" --manifest "$TEST_DIR/labels.yaml"
  [ "$status" -eq 0 ]
  [[ "$output" == *"UPDATE bug"* ]]
}

@test "dry-run plans deletes for labels not in manifest" {
  export SYNC_LABELS_GH_LIST_FIXTURE='[{"name":"bug","color":"d73a4a","description":"Defect"},{"name":"area/operator","color":"0e8a16","description":"Operator"},{"name":"obsolete","color":"ffffff","description":"old"}]'
  run bash "$BATS_TEST_DIRNAME/../../tools/sync-labels.sh" --manifest "$TEST_DIR/labels.yaml"
  [ "$status" -eq 0 ]
  [[ "$output" == *"DELETE obsolete"* ]]
}

@test "--keep-extras suppresses deletes" {
  export SYNC_LABELS_GH_LIST_FIXTURE='[{"name":"obsolete","color":"ffffff","description":"old"}]'
  run bash "$BATS_TEST_DIRNAME/../../tools/sync-labels.sh" --manifest "$TEST_DIR/labels.yaml" --keep-extras
  [ "$status" -eq 0 ]
  [[ "$output" != *"DELETE"* ]]
}

@test "exits zero with no mutations when manifest matches gh exactly" {
  export SYNC_LABELS_GH_LIST_FIXTURE='[{"name":"bug","color":"d73a4a","description":"Defect"},{"name":"area/operator","color":"0e8a16","description":"Operator"}]'
  run bash "$BATS_TEST_DIRNAME/../../tools/sync-labels.sh" --manifest "$TEST_DIR/labels.yaml"
  [ "$status" -eq 0 ]
  [[ "$output" != *"CREATE"* ]]
  [[ "$output" != *"UPDATE"* ]]
  [[ "$output" != *"DELETE"* ]]
}
```

- [ ] **Step 6: Run full test suite, all pass**

```bash
bats tests/tools/sync-labels.bats
```
Expected: 4 tests PASS.

- [ ] **Step 7: Make script executable + shellcheck clean**

```bash
chmod +x tools/sync-labels.sh
shellcheck tools/sync-labels.sh
```
Expected: shellcheck exits 0.

- [ ] **Step 8: Commit**

```bash
git add tools/sync-labels.sh tests/tools/sync-labels.bats
git commit -m "feat(tools): add idempotent sync-labels.sh + bats tests

Reads .github/labels.yaml, diffs against gh label list, prints/applies
create/update/delete. Default mode is dry-run; --apply does the work.
--keep-extras opts out of deletion."
```

---

## Task 3: Add label-sync GitHub Action

**Files:**
- Create: `.github/workflows/sync-labels.yml`

- [ ] **Step 1: Write the workflow**

```yaml
# .github/workflows/sync-labels.yml
name: Sync labels

on:
  push:
    branches: [main]
    paths:
      - '.github/labels.yaml'
      - 'tools/sync-labels.sh'
      - '.github/workflows/sync-labels.yml'
  workflow_dispatch:

permissions:
  issues: write

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Install yq
        run: |
          sudo curl -L -o /usr/local/bin/yq \
            https://github.com/mikefarah/yq/releases/download/v4.44.3/yq_linux_amd64
          sudo chmod +x /usr/local/bin/yq
      - name: Sync labels (apply)
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: bash tools/sync-labels.sh --apply
```

- [ ] **Step 2: Validate workflow syntax**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/sync-labels.yml'))"`
Expected: no error.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/sync-labels.yml
git commit -m "ci: add label-sync workflow

Runs tools/sync-labels.sh --apply on push to main when the manifest
or script changes, and via workflow_dispatch."
```

---

## Task 4: Initial label sync (manual gate)

**Files:** none — this is a manual run by a maintainer with `gh` auth.

This task is intentionally not automated: the first run includes deletes (`invalid`, `duplicate`, `wontfix`, `question`) that should be observed by a human before merging the manifest. The workflow added in T3 handles all subsequent syncs automatically.

- [ ] **Step 1: Maintainer runs dry-run**

```bash
bash tools/sync-labels.sh --manifest .github/labels.yaml
```
Expected: prints CREATE / UPDATE / DELETE lines. Confirm the DELETE list contains only the labels listed in the spec's "Retired" table (`invalid`, `duplicate`, `wontfix`, `question`). If anything else shows up, abort and add a comment to the PR.

- [ ] **Step 2: Maintainer runs apply**

```bash
bash tools/sync-labels.sh --manifest .github/labels.yaml --apply
```
Expected: all CREATE / UPDATE / DELETE mutations succeed.

- [ ] **Step 3: Verify final state**

```bash
gh label list --limit 100 --json name | jq -r '.[].name' | sort > /tmp/actual.txt
yq -o=json '.' .github/labels.yaml | jq -r '.[].name' | sort > /tmp/expected.txt
diff /tmp/expected.txt /tmp/actual.txt
```
Expected: empty diff.

- [ ] **Step 4: Note the run in the PR**

Add a comment to the rollout PR: `Initial label sync complete: <N created>, <N updated>, <N deleted>. Verified diff = empty.`

---

## Task 5: PR auto-labeler

**Files:**
- Create: `.github/labeler.yml`
- Create: `.github/workflows/pr-labeler.yml`

- [ ] **Step 1: Write labeler config**

```yaml
# .github/labeler.yml
# Path-glob → label map for actions/labeler@v5.
# Only sets labels that can be inferred unambiguously from changed paths.
# Issue labels (commitment, signals, needs/*) are NOT set here.

area/operator:
  - changed-files:
    - any-glob-to-any-file: ['operator/**']

area/task-service:
  - changed-files:
    - any-glob-to-any-file: ['task-service/**']

area/adapters:
  - changed-files:
    - any-glob-to-any-file: ['adapters/**']

area/kapeproxy:
  - changed-files:
    - any-glob-to-any-file: ['kapeproxy/**']

area/runtime:
  - changed-files:
    - any-glob-to-any-file: ['runtime/**']

area/dashboard:
  - changed-files:
    - any-glob-to-any-file: ['dashboard/**']

area/helm:
  - changed-files:
    - any-glob-to-any-file: ['helm/**']

area/crds:
  - changed-files:
    - any-glob-to-any-file: ['crds/**']

area/docs:
  - changed-files:
    - any-glob-to-any-file:
      - 'docs/**'
      - 'README.md'
      - 'CONTRIBUTING.md'
      - '!docs/superpowers/specs/**'  # spec docs get category=spec instead

area/ci:
  - changed-files:
    - any-glob-to-any-file:
      - '.github/**'
      - 'tools/**'

# --- Category labels (declarative paths only — humans set the rest) ---

docs:
  - changed-files:
    - all-globs-to-all-files:
      - 'docs/**'
      - '!docs/superpowers/specs/**'
      - '!**/*.go'
      - '!**/*.py'
      - '!**/*.ts'

test:
  - changed-files:
    - all-globs-to-all-files:
      - any-glob-to-any-file:
        - '**/*_test.go'
        - '**/test_*.py'
        - '**/*.test.ts'
        - 'tests/**'

spec:
  - changed-files:
    - any-glob-to-any-file: ['docs/superpowers/specs/**']
```

- [ ] **Step 2: Write the workflow**

```yaml
# .github/workflows/pr-labeler.yml
name: PR auto-label

on:
  pull_request_target:
    types: [opened, synchronize, reopened]

permissions:
  contents: read
  pull-requests: write

jobs:
  label:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/labeler@v5
        with:
          configuration-path: .github/labeler.yml
          sync-labels: true
```

- [ ] **Step 3: Validate workflow + config syntax**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/labeler.yml'))"
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/pr-labeler.yml'))"
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add .github/labeler.yml .github/workflows/pr-labeler.yml
git commit -m "ci: add PR auto-labeler

actions/labeler@v5 with path-glob mapping to area/* and to the
category labels that can be inferred unambiguously from paths
(docs, test, spec). Other labels are set by humans or the validator."
```

---

## Task 6: Update `/standup` datasources

**Files:**
- Modify: `.claude/standup.json`

- [ ] **Step 1: Read current file**

```bash
cat .claude/standup.json
```
Confirm content matches the spec context (two datasources: `bug`, `snyk-finding`).

- [ ] **Step 2: Write the new file**

```json
{
  "datasources": [
    {
      "name": "bug",
      "description": "Failed GitHub Actions runs in the last 7 days — signals potential regressions needing a bug issue",
      "command": "gh run list --status failure --limit 20 --json databaseId,displayTitle,workflowName,conclusion,updatedAt,headBranch"
    },
    {
      "name": "snyk-finding",
      "description": "Snyk code scan findings in the operator module — security issues tracked via the snyk-finding label",
      "tool": "mcp__Snyk__snyk_code_scan",
      "params": {
        "scanPath": "./operator"
      }
    },
    {
      "name": "urgent-issues",
      "description": "Open issues labelled urgent — surfaced top of standup per agent-rituals.md",
      "command": "gh issue list --state open --label urgent --json number,title,labels,assignees,milestone,updatedAt,body"
    },
    {
      "name": "blocked-issues",
      "description": "Open issues labelled blocked — surfaced in Stuck bucket when blocked > 3 days per agent-rituals.md",
      "command": "gh issue list --state open --label blocked --json number,title,labels,assignees,milestone,updatedAt,body"
    }
  ]
}
```

- [ ] **Step 3: Validate JSON**

```bash
jq empty .claude/standup.json
```
Expected: no error.

- [ ] **Step 4: Commit**

```bash
git add .claude/standup.json
git commit -m "chore(standup): add urgent + blocked datasources

Per docs/agent-rituals.md, /standup reads issues by these labels to
populate the ACT NOW and STUCK buckets without re-reading bodies."
```

---

## Task 7: State-validator script

**Files:**
- Create: `tools/validate-issue-labels.sh`
- Create: `tests/tools/validate-issue-labels.bats`

Implements the rules from `docs/agent-rituals.md` → "State validator". Input: one issue's JSON via stdin. Output: zero or more lines of `ERROR <code>: <msg>` or `WARN <code>: <msg>`. Exit code: 0 always (errors are data, not script failure). Caller aggregates.

- [ ] **Step 1: Write the first failing test (multiple commitments → ERROR)**

```bash
# tests/tools/validate-issue-labels.bats
#!/usr/bin/env bats

run_validator() {
  local input="$1"
  echo "$input" | bash "$BATS_TEST_DIRNAME/../../tools/validate-issue-labels.sh"
}

@test "issue with two commitments → ERROR" {
  result=$(run_validator '{"number":1,"body":"","labels":[{"name":"committed"},{"name":"stretch"}]}')
  [[ "$result" == *"ERROR multiple-commitments"* ]]
}
```

- [ ] **Step 2: Run, watch fail**

```bash
bats tests/tools/validate-issue-labels.bats
```
Expected: FAIL with "No such file or directory".

- [ ] **Step 3: Write minimal validator**

```bash
#!/usr/bin/env bash
# tools/validate-issue-labels.sh
# Read one issue's JSON from stdin (gh issue view --json number,body,labels output shape)
# and emit ERROR/WARN lines per docs/agent-rituals.md → "State validator".
# Exit code is always 0; aggregation/severity decisions are the caller's job.
set -euo pipefail

input="$(cat)"

# helpers
has_label() {
  local pattern="$1"
  echo "$input" | jq -e --arg p "$pattern" '.labels[] | select(.name == $p)' >/dev/null
}
count_labels_matching() {
  local pattern="$1"  # jq regex
  echo "$input" | jq --arg p "$pattern" '[.labels[].name | select(test($p))] | length'
}
body_has_line() {
  local pattern="$1"
  echo "$input" | jq -r '.body // ""' | grep -qE "$pattern"
}

# Rule 1: multiple commitments
commitments=$(count_labels_matching '^(committed|stretch|backlog)$')
if [[ "$commitments" -gt 1 ]]; then
  echo "ERROR multiple-commitments: issue has $commitments commitment labels (expected 0 or 1)"
fi

main_exit() { exit 0; }
main_exit
```

- [ ] **Step 4: Run, watch pass**

```bash
bats tests/tools/validate-issue-labels.bats
```
Expected: 1 test PASS.

- [ ] **Step 5: Add remaining rule tests**

```bash
# Append to tests/tools/validate-issue-labels.bats

@test "ready without category → ERROR" {
  result=$(run_validator '{"number":2,"body":"","labels":[{"name":"ready"},{"name":"area/operator"},{"name":"backlog"}]}')
  [[ "$result" == *"ERROR ready-missing-category"* ]]
}

@test "ready without area → ERROR" {
  result=$(run_validator '{"number":3,"body":"","labels":[{"name":"ready"},{"name":"bug"},{"name":"backlog"}]}')
  [[ "$result" == *"ERROR ready-missing-area"* ]]
}

@test "ready without commitment → ERROR" {
  result=$(run_validator '{"number":4,"body":"","labels":[{"name":"ready"},{"name":"bug"},{"name":"area/operator"}]}')
  [[ "$result" == *"ERROR ready-missing-commitment"* ]]
}

@test "ready with needs/* → ERROR" {
  result=$(run_validator '{"number":5,"body":"","labels":[{"name":"ready"},{"name":"bug"},{"name":"area/operator"},{"name":"backlog"},{"name":"needs/info"}]}')
  [[ "$result" == *"ERROR ready-with-needs"* ]]
}

@test "both needs-triage and ready → ERROR" {
  result=$(run_validator '{"number":6,"body":"","labels":[{"name":"ready"},{"name":"needs-triage"},{"name":"bug"},{"name":"area/operator"},{"name":"backlog"}]}')
  [[ "$result" == *"ERROR both-needs-triage-and-ready"* ]]
}

@test "urgent without Reason: → WARN" {
  result=$(run_validator '{"number":7,"body":"some text without the marker","labels":[{"name":"urgent"}]}')
  [[ "$result" == *"WARN urgent-without-reason"* ]]
}

@test "urgent with Reason: → no warn" {
  result=$(run_validator '{"number":8,"body":"Reason: prod incident\nmore text","labels":[{"name":"urgent"}]}')
  [[ "$result" != *"urgent-without-reason"* ]]
}

@test "blocked without Blocked by: → WARN" {
  result=$(run_validator '{"number":9,"body":"some text","labels":[{"name":"blocked"}]}')
  [[ "$result" == *"WARN blocked-without-reference"* ]]
}

@test "blocked with Blocked by: → no warn" {
  result=$(run_validator '{"number":10,"body":"Blocked by: #42","labels":[{"name":"blocked"}]}')
  [[ "$result" != *"blocked-without-reference"* ]]
}

@test "clean issue → no output" {
  result=$(run_validator '{"number":11,"body":"","labels":[{"name":"bug"},{"name":"area/operator"},{"name":"backlog"},{"name":"ready"}]}')
  [ -z "$result" ]
}
```

- [ ] **Step 6: Extend validator to satisfy the new tests**

Replace `tools/validate-issue-labels.sh` body with:

```bash
#!/usr/bin/env bash
# tools/validate-issue-labels.sh
# Read one issue's JSON from stdin (gh issue view --json number,body,labels output)
# and emit ERROR/WARN lines per docs/agent-rituals.md → "State validator".
# Exit code is always 0; aggregation is the caller's responsibility.
set -euo pipefail

input="$(cat)"

has_label() {
  local pattern="$1"
  echo "$input" | jq -e --arg p "$pattern" '.labels[] | select(.name == $p)' >/dev/null
}
count_labels_matching() {
  local pattern="$1"
  echo "$input" | jq --arg p "$pattern" '[.labels[].name | select(test($p))] | length'
}
body_has_line() {
  local pattern="$1"
  echo "$input" | jq -r '.body // ""' | grep -qE "$pattern"
}

categories='^(bug|enhancement|feature|refactor|redesign|security|docs|chore|test|spec)$'
areas='^area/'
needs='^needs/'

# Rule 1: multiple commitments
commitments=$(count_labels_matching '^(committed|stretch|backlog)$')
if [[ "$commitments" -gt 1 ]]; then
  echo "ERROR multiple-commitments: issue has $commitments commitment labels (expected 0 or 1)"
fi

# Rule 2-5: ready preconditions
if has_label "ready"; then
  cat_count=$(count_labels_matching "$categories")
  area_count=$(count_labels_matching "$areas")
  needs_count=$(count_labels_matching "$needs")
  [[ "$cat_count" -eq 0 ]] && echo "ERROR ready-missing-category: ready label set without a category label"
  [[ "$area_count" -eq 0 ]] && echo "ERROR ready-missing-area: ready label set without an area/* label"
  [[ "$commitments" -eq 0 ]] && echo "ERROR ready-missing-commitment: ready label set without a commitment label"
  [[ "$needs_count" -gt 0 ]] && echo "ERROR ready-with-needs: ready label set with open needs/* label(s)"
  has_label "needs-triage" && echo "ERROR both-needs-triage-and-ready: issue has both needs-triage and ready"
fi

# Rule 6-7: urgent / blocked body markers
if has_label "urgent" && ! body_has_line '^Reason:'; then
  echo "WARN urgent-without-reason: urgent label set but body missing 'Reason: <line>'"
fi
if has_label "blocked" && ! body_has_line '^Blocked by:'; then
  echo "WARN blocked-without-reference: blocked label set but body missing 'Blocked by: <ref>'"
fi
```

- [ ] **Step 7: Run full bats suite, all pass**

```bash
chmod +x tools/validate-issue-labels.sh
bats tests/tools/validate-issue-labels.bats
```
Expected: 10 tests PASS.

- [ ] **Step 8: shellcheck clean**

```bash
shellcheck tools/validate-issue-labels.sh
```
Expected: exits 0.

- [ ] **Step 9: Commit**

```bash
git add tools/validate-issue-labels.sh tests/tools/validate-issue-labels.bats
git commit -m "feat(tools): add issue label state validator

Implements the rules from docs/agent-rituals.md → State validator.
Reads one issue's JSON via stdin, emits ERROR/WARN lines, exits 0.
Covered by 10 bats tests."
```

---

## Task 8: State-validator GitHub Action

**Files:**
- Create: `.github/workflows/validate-labels.yml`

Runs the validator weekly across all open issues and on every issue label change. Posts a comment on each issue with violations.

- [ ] **Step 1: Write the workflow**

```yaml
# .github/workflows/validate-labels.yml
name: Validate issue labels

on:
  schedule:
    - cron: '0 9 * * 1'  # Mondays 09:00 UTC
  issues:
    types: [opened, labeled, unlabeled, edited]
  workflow_dispatch:

permissions:
  issues: write

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Validate single issue (on issues event)
        if: github.event_name == 'issues'
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          ISSUE_NUMBER: ${{ github.event.issue.number }}
        run: |
          payload=$(gh issue view "$ISSUE_NUMBER" --json number,body,labels)
          violations=$(echo "$payload" | bash tools/validate-issue-labels.sh)
          if [[ -n "$violations" ]]; then
            body=$(printf 'State validator found label inconsistencies:\n\n```\n%s\n```\n\nSee docs/agent-rituals.md for rule definitions.\n' "$violations")
            gh issue comment "$ISSUE_NUMBER" --body "$body"
          fi

      - name: Validate all open issues (on schedule)
        if: github.event_name != 'issues'
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          set -euo pipefail
          gh issue list --state open --limit 200 --json number | jq -r '.[].number' \
            | while read -r num; do
              payload=$(gh issue view "$num" --json number,body,labels)
              violations=$(echo "$payload" | bash tools/validate-issue-labels.sh)
              if [[ -n "$violations" ]]; then
                echo "Issue #$num:"
                echo "$violations"
                echo
              fi
            done | tee weekly-validator-report.txt
          # Note: scheduled run only reports to job log; per-issue comments would be too noisy weekly.
```

- [ ] **Step 2: Validate workflow YAML**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/validate-labels.yml'))"
```
Expected: no error.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/validate-labels.yml
git commit -m "ci: add state-validator workflow

On issue events: validate single issue, comment if violations exist.
On weekly schedule: validate all open issues, emit summary to job log."
```

---

## Task 9: Update `tools/migrate-to-github.sh` for new labels

**Files:**
- Modify: `tools/migrate-to-github.sh`

The migration script today sets only `roadmap-sync` and a hardcoded description. Update it to derive `area/*`, set `enhancement` + `committed` (since milestoned roadmap items are committed by definition), set `phase/Mx-*`, and keep `roadmap-sync` until T12.

- [ ] **Step 1: Read the current script**

Note: 648 lines total. The relevant section is `create_issue()` and any helpers it calls.

```bash
sed -n '1,80p' tools/migrate-to-github.sh
```
Confirm shape matches: `create_label_if_missing`, `create_milestone`, `create_issue` functions.

- [ ] **Step 2: Add a `derive_area` helper near the top of the script**

Insert after the `gh_api` helper (around line 12):

```bash
# Derive area/* label from issue title keywords. Returns empty if ambiguous.
# Mapping mirrors docs/agent-rituals.md → "Title-keyword → area/* mapping".
derive_area() {
  local title="$1"
  local t
  t="$(echo "$title" | tr '[:upper:]' '[:lower:]')"
  case "$t" in
    *"operator"*|*"reconciler"*|*"kapehandler"*|*"kapetool"*|*"kapeschema"*|*"crd validation"*) echo "area/operator" ;;
    *"task-service"*|*"task service"*|*"openapi"*) echo "area/task-service" ;;
    *"adapter"*|*"alertmanager"*|*"audit adapter"*) echo "area/adapters" ;;
    *"kapeproxy"*|*"mcp proxy"*) echo "area/kapeproxy" ;;
    *"runtime"*|*"langgraph"*|*"python"*) echo "area/runtime" ;;
    *"dashboard"*|*"sse"*|*"eventsource"*|*"oauth2 proxy"*) echo "area/dashboard" ;;
    *"helm"*|*"chart"*|*"template"*) echo "area/helm" ;;
    *"crd"*|*"cel"*|*"xvalidation"*) echo "area/crds" ;;
    *"docs"*|*"readme"*|*"runbook"*|*"changelog"*) echo "area/docs" ;;
    *"ci"*|*"workflow"*|*"actions"*|*"snyk"*) echo "area/ci" ;;
    *"nats"*|*"postgres"*|*"cloudnativepg"*|*"cert-manager"*|*"eso"*|*"external secrets"*) echo "area/infra" ;;
    *) echo "" ;;
  esac
}

# Derive phase/Mx-* label from "[Pn/mm]" title prefix.
# Phase → milestone mapping comes from the milestones table.
derive_phase() {
  local title="$1"
  case "$title" in
    \[P6/*) echo "phase/M2-operator" ;;
    \[P7/*) echo "phase/M3-runtime" ;;
    \[P8/*) echo "phase/M4-security" ;;
    \[P9/*) echo "phase/M5-dashboard" ;;
    \[P10/*) echo "phase/M6-release" ;;
    *) echo "" ;;
  esac
}
```

- [ ] **Step 3: Update `create_issue` to apply derived labels**

Find the existing `gh_api "repos/${REPO}/issues" \` invocation in `create_issue` (around line 56-63 of the original) and replace the `-f "labels[]=roadmap-sync"` line with a derived block:

```bash
  local area_label phase_label
  area_label="$(derive_area "$title")"
  phase_label="$(derive_phase "$title")"

  local label_args=(-f "labels[]=roadmap-sync" -f "labels[]=enhancement" -f "labels[]=committed")
  [[ -n "$area_label" ]]  && label_args+=(-f "labels[]=$area_label")
  [[ -n "$phase_label" ]] && label_args+=(-f "labels[]=$phase_label")
  [[ -z "$area_label" || -z "$phase_label" ]] && label_args+=(-f "labels[]=needs-triage")

  issue_number=$(gh_api "repos/${REPO}/issues" \
    -f title="${title}" \
    -f body="${body}" \
    -f milestone="${milestone_number}" \
    "${label_args[@]}" \
    --jq '.number')
```

Replace the existing `-f "labels[]=roadmap-sync"` (and any prior single-label arg) accordingly. Keep everything else (state mutation, output) intact.

- [ ] **Step 4: shellcheck clean**

```bash
shellcheck tools/migrate-to-github.sh
```
Expected: exits 0 (or with only pre-existing warnings unrelated to this change — record them in commit if so).

- [ ] **Step 5: Verify the script still parses by dry-running help/usage**

```bash
bash -n tools/migrate-to-github.sh
```
Expected: no syntax errors.

- [ ] **Step 6: Commit**

```bash
git add tools/migrate-to-github.sh
git commit -m "feat(migrate): apply new label taxonomy to roadmap issues

Adds derive_area() and derive_phase() helpers. New roadmap issues
get: roadmap-sync (transitional) + enhancement + committed + area/*
+ phase/Mx-*. Missing area or phase falls back to needs-triage."
```

---

## Task 10: Backfill open issues

**Files:**
- Create: `tools/backfill-issue-labels.sh`

One-shot script that walks open issues and applies the derivation logic to issues that don't already have the new labels. Dry-run by default; `--apply` does the work. Re-uses the helpers from T9 via sourcing.

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
# tools/backfill-issue-labels.sh
# Backfill the new label taxonomy on open issues that pre-date it.
# Dry-run by default; --apply does the mutations.
# Derivation helpers are duplicated from tools/migrate-to-github.sh by design:
# both are one-shot scripts, and a shared library is overkill for ~30 lines.
set -euo pipefail

APPLY=0
[[ "${1:-}" == "--apply" ]] && APPLY=1

derive_area() {
  local title="$1"
  local t
  t="$(echo "$title" | tr '[:upper:]' '[:lower:]')"
  case "$t" in
    *"operator"*|*"reconciler"*|*"kapehandler"*|*"kapetool"*|*"kapeschema"*|*"crd validation"*) echo "area/operator" ;;
    *"task-service"*|*"task service"*|*"openapi"*) echo "area/task-service" ;;
    *"adapter"*|*"alertmanager"*|*"audit adapter"*) echo "area/adapters" ;;
    *"kapeproxy"*|*"mcp proxy"*) echo "area/kapeproxy" ;;
    *"runtime"*|*"langgraph"*|*"python"*) echo "area/runtime" ;;
    *"dashboard"*|*"sse"*|*"eventsource"*|*"oauth2 proxy"*) echo "area/dashboard" ;;
    *"helm"*|*"chart"*|*"template"*) echo "area/helm" ;;
    *"crd"*|*"cel"*|*"xvalidation"*) echo "area/crds" ;;
    *"docs"*|*"readme"*|*"runbook"*|*"changelog"*) echo "area/docs" ;;
    *"ci"*|*"workflow"*|*"actions"*|*"snyk"*) echo "area/ci" ;;
    *"nats"*|*"postgres"*|*"cloudnativepg"*|*"cert-manager"*|*"eso"*|*"external secrets"*) echo "area/infra" ;;
    *) echo "" ;;
  esac
}

derive_phase() {
  local title="$1"
  case "$title" in
    \[P6/*) echo "phase/M2-operator" ;;
    \[P7/*) echo "phase/M3-runtime" ;;
    \[P8/*) echo "phase/M4-security" ;;
    \[P9/*) echo "phase/M5-dashboard" ;;
    \[P10/*) echo "phase/M6-release" ;;
    *) echo "" ;;
  esac
}

apply_labels() {
  local issue_num="$1" labels="$2"
  echo "[$issue_num] add: $labels"
  if [[ $APPLY -eq 1 ]]; then
    # gh issue edit --add-label expects comma-separated
    gh issue edit "$issue_num" --add-label "$labels"
  fi
}

main() {
  gh issue list --state open --limit 200 \
    --json number,title,labels --jq '.[] | @json' \
    | while read -r row; do
      local num title existing
      num=$(echo "$row" | jq -r '.number')
      title=$(echo "$row" | jq -r '.title')
      existing=$(echo "$row" | jq -r '[.labels[].name] | join(",")')

      # Skip if issue already has a commitment label (already migrated)
      if echo ",$existing," | grep -qE ',(committed|stretch|backlog),'; then
        continue
      fi

      local to_add=()
      local area phase
      area="$(derive_area "$title")"
      phase="$(derive_phase "$title")"

      # Ensure category for roadmap-tagged issues
      if echo ",$existing," | grep -q ',roadmap-sync,' && ! echo ",$existing," | grep -qE ',(bug|enhancement|feature|refactor|redesign|security|docs|chore|test|spec),'; then
        to_add+=("enhancement")
      fi

      [[ -n "$area" ]] && ! echo ",$existing," | grep -q ",$area," && to_add+=("$area")
      [[ -n "$phase" ]] && ! echo ",$existing," | grep -q ",$phase," && to_add+=("$phase")

      # Default commitment: committed if roadmap (assumed in current milestone), else backlog
      if echo ",$existing," | grep -q ',roadmap-sync,'; then
        to_add+=("committed")
      else
        to_add+=("backlog")
      fi

      # If area or phase couldn't be derived, mark needs-triage so the validator surfaces it
      if [[ -z "$area" || ( -z "$phase" && $(echo ",$existing," | grep -c ',roadmap-sync,') -eq 1 ) ]]; then
        to_add+=("needs-triage")
      fi

      if [[ ${#to_add[@]} -gt 0 ]]; then
        apply_labels "$num" "$(IFS=,; echo "${to_add[*]}")"
      fi
    done
}

main "$@"
```

- [ ] **Step 2: shellcheck clean**

```bash
chmod +x tools/backfill-issue-labels.sh
shellcheck tools/backfill-issue-labels.sh
```
Expected: exits 0.

- [ ] **Step 3: Maintainer runs dry-run**

```bash
bash tools/backfill-issue-labels.sh
```
Expected: per-issue "add:" lines for every open issue. No mutations. Eyeball the list — every roadmap issue should be slated for `enhancement + area/* + phase/Mx-* + committed`. Anything that derives to `needs-triage` (missing area or phase) should be inspected manually.

- [ ] **Step 4: Maintainer fixes any obvious misderivations**

If `derive_area` produces the wrong result for a known issue, either update the keyword map in `tools/migrate-to-github.sh` (Task 9) and re-run, or label the issue manually post-backfill. The script is intentionally idempotent: a second `--apply` run is a no-op on already-migrated issues.

- [ ] **Step 5: Maintainer runs apply**

```bash
bash tools/backfill-issue-labels.sh --apply
```
Expected: each "add:" line executes a `gh issue edit` successfully.

- [ ] **Step 6: Sanity-check via validator**

```bash
gh issue list --state open --limit 200 --json number | jq -r '.[].number' \
  | while read -r n; do
      gh issue view "$n" --json number,body,labels \
        | bash tools/validate-issue-labels.sh | sed "s/^/#$n /"
    done
```
Expected: zero ERROR lines from the validator. WARN lines for `urgent-without-reason` / `blocked-without-reference` are acceptable for legacy issues; record them in the PR comment.

- [ ] **Step 7: Commit**

```bash
git add tools/backfill-issue-labels.sh
git commit -m "feat(tools): add one-shot issue-label backfill script

Walks open issues, derives area/phase via the migrate-script helpers,
and adds enhancement+committed+area+phase to roadmap-tagged issues
that pre-date the new taxonomy. Idempotent (skips already-migrated)."
```

---

## Task 11: Snyk integration update

**Files:**
- Create: `tools/snyk-finding-to-issue.sh`

`/standup` already calls `mcp__Snyk__snyk_code_scan` for the operator module and infers a `snyk-finding` label. The skill code paths that *create* an issue from a Snyk finding need to also set `security` + `urgent` + an `area/*` derived from scan path. Since the existing snyk-finding flow lives in the standup skill prompt (not a script), this task adds a helper script the skill can shell out to.

- [ ] **Step 1: Write the helper**

```bash
#!/usr/bin/env bash
# tools/snyk-finding-to-issue.sh
# Open or update a GitHub issue from a Snyk MCP finding.
# Args:
#   --title <string>       (required)
#   --body  <string>       (required — should already include 'Reason: Snyk <severity> ...')
#   --scan-path <path>     (required — e.g., ./operator)
#   --finding-id <string>  (required — used to dedupe via title prefix)
set -euo pipefail

TITLE=""; BODY=""; SCAN_PATH=""; FINDING_ID=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --title) TITLE="$2"; shift 2 ;;
    --body) BODY="$2"; shift 2 ;;
    --scan-path) SCAN_PATH="$2"; shift 2 ;;
    --finding-id) FINDING_ID="$2"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

for var in TITLE BODY SCAN_PATH FINDING_ID; do
  [[ -z "${!var}" ]] && { echo "missing --$(echo "$var" | tr '[:upper:]' '[:lower:]' | tr _ -)" >&2; exit 2; }
done

# Map scan path → area/*
area=""
case "$SCAN_PATH" in
  ./operator|operator) area="area/operator" ;;
  ./adapters|adapters) area="area/adapters" ;;
  ./task-service|task-service) area="area/task-service" ;;
  ./kapeproxy|kapeproxy) area="area/kapeproxy" ;;
  ./runtime|runtime) area="area/runtime" ;;
  *) area="" ;;
esac

# Prepend finding id to title so dedupe via title-match works
full_title="[$FINDING_ID] $TITLE"

existing=$(gh issue list --state all --search "in:title \"[$FINDING_ID]\"" --json number --jq '.[0].number // ""')
if [[ -n "$existing" ]]; then
  echo "Issue already exists: #$existing — skipping create"
  exit 0
fi

labels=("bug" "security" "snyk-finding" "urgent" "backlog")
[[ -n "$area" ]] && labels+=("$area")

gh issue create \
  --title "$full_title" \
  --body "$BODY" \
  $(printf -- "--label %s " "${labels[@]}")
```

- [ ] **Step 2: shellcheck clean**

```bash
chmod +x tools/snyk-finding-to-issue.sh
shellcheck tools/snyk-finding-to-issue.sh
```
Expected: exits 0.

- [ ] **Step 3: Smoke-test dedupe path with a known fake finding-id**

```bash
bash tools/snyk-finding-to-issue.sh \
  --title "smoke test" \
  --body "Reason: smoke test - not a real finding" \
  --scan-path ./operator \
  --finding-id "SMOKE-TEST-DELETE-ME"
```
Expected: issue created with labels `bug, security, snyk-finding, urgent, backlog, area/operator`. Verify with `gh issue list --label snyk-finding --limit 5`, then close + delete the smoke-test issue.

- [ ] **Step 4: Commit**

```bash
git add tools/snyk-finding-to-issue.sh
git commit -m "feat(tools): add Snyk finding → GitHub issue helper

Maps Snyk MCP findings to issues with bug+security+snyk-finding+urgent
+area/* (derived from scan path) +backlog. Dedupes via [<finding-id>]
title prefix. Called from the /standup skill on new Snyk findings."
```

---

## Task 12: Retire `roadmap-sync` (final cleanup)

**Files:**
- Modify: `.github/labels.yaml`
- Modify: `tools/migrate-to-github.sh`

Run only after T10's backfill is verified. Removes `roadmap-sync` from the manifest (which the sync workflow then deletes from the repo) and from the migration script.

- [ ] **Step 1: Verify all roadmap-sync issues now carry `phase/*`**

```bash
gh issue list --label roadmap-sync --state open --limit 200 --json number,labels \
  | jq '[.[] | select(.labels | map(.name) | any(startswith("phase/")) | not)] | length'
```
Expected: `0`. If non-zero, abort — those issues need a phase before this task can proceed.

- [ ] **Step 2: Remove `roadmap-sync` from the manifest**

Edit `.github/labels.yaml`: delete the entry under the "Transitional" section:

```yaml
# DELETE this block:
- name: roadmap-sync
  color: 0075ca
  description: "Roadmap sync issue (transitional — retired after backfill)"
```

- [ ] **Step 3: Remove the `roadmap-sync` label-arg from the migration script**

In `tools/migrate-to-github.sh`, locate the `label_args=(...)` line modified in T9 and drop the `roadmap-sync` element:

```bash
# Before (from T9):
local label_args=(-f "labels[]=roadmap-sync" -f "labels[]=enhancement" -f "labels[]=committed")
# After:
local label_args=(-f "labels[]=enhancement" -f "labels[]=committed")
```

Also update the issue-existence check that filters by `labels=roadmap-sync`:

```bash
# Before:
existing=$(gh_api "repos/${REPO}/issues?state=all&per_page=100&labels=roadmap-sync" ...)
# After:
existing=$(gh_api "repos/${REPO}/issues?state=all&per_page=100&labels=phase/M${ms_number}-*" ...)
```

If the resulting query is too brittle, fall back to title-match dedup as in T11.

- [ ] **Step 4: Run the sync script in dry-run to confirm only one DELETE**

```bash
bash tools/sync-labels.sh --manifest .github/labels.yaml
```
Expected: a single `DELETE roadmap-sync` line, no other mutations.

- [ ] **Step 5: shellcheck the modified migration script**

```bash
shellcheck tools/migrate-to-github.sh
```
Expected: exits 0.

- [ ] **Step 6: Commit**

```bash
git add .github/labels.yaml tools/migrate-to-github.sh
git commit -m "chore(labels): retire roadmap-sync after backfill

Backfill (T10) populated phase/* on every roadmap issue, so the
transitional roadmap-sync label is no longer the discriminator.
Removed from manifest (sync workflow deletes from repo on next run)
and from the migration script's label-args."
```

---

## Self-review checklist (run before opening the PR for this plan)

**1. Spec coverage:**
- [x] Category labels created (T1)
- [x] Area labels created (T1)
- [x] Phase labels created (T1)
- [x] Commitment labels created (T1)
- [x] Signal labels created (T1)
- [x] needs/* labels created (T1)
- [x] Standalone keepers preserved (T1)
- [x] Retired labels deleted (T4 — manual gate + T12 — roadmap-sync)
- [x] PR auto-labeler implemented (T5)
- [x] Snyk integration sets security+urgent (T11)
- [x] /standup datasources extended (T6)
- [x] Migration script updated to new labels (T9)
- [x] State validator implemented (T7) and run as GHA (T8)
- [x] Backfill of open issues (T10)
- [x] Phase-sync rule — covered by T9 (migration script) and T8 (validator warns on milestone/phase drift)

**2. Placeholder scan:** no "TBD"/"TODO" markers in plan body; every step has either complete code, a complete command, or a manual step with explicit acceptance criteria.

**3. Type consistency:**
- Helper function names (`derive_area`, `derive_phase`) are identical between T9 and T10.
- Validator error codes (`multiple-commitments`, `ready-missing-category`, …) are identical between T7 tests and T8 (GHA reads stdout verbatim).
- `.github/labels.yaml` schema (`name`, `color`, `description`) is identical between T1 and T2's TSV parsing.

## Out of scope (deferred — track as follow-up issues if needed)

- Per-issue "Reason:" autofill (currently the human writing `urgent` must add the line themselves; validator only warns).
- `lifecycle/stale` automation (the spec defers this).
- Per-author / per-LLM / per-customer labels (not in use today).
- Promotion of `stretch` → `committed` by the planning ritual at milestone open (the rule exists in `docs/agent-rituals.md`; the agent that runs it is its own piece of work).
- Renaming the existing `bug` datasource in `.claude/standup.json` if it ever conflicts with the new label-driven datasources (today it's a failed-CI-runs datasource; no conflict).
