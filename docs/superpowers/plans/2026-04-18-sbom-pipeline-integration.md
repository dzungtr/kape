# SBOM Pipeline Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate per-module CycloneDX SBOM generation into the Claude Code PR workflow via a project CLAUDE.md instruction and protect against accidental SBOM artifact commits via `.gitignore`.

**Architecture:** A project-level `CLAUDE.md` instructs Claude to call `snyk_sbom_scan` (Snyk MCP tool) on each of the three Go modules before raising any PR, then post a markdown summary as a PR comment. No shell hooks, no Makefile targets — purely a behavioral instruction executed in Claude's own context.

**Tech Stack:** Snyk MCP (`snyk_sbom_scan`), GitHub CLI (`gh pr comment`), Git

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `CLAUDE.md` | Create | Project-level behavioral instructions for Claude, including PR checklist |
| `.gitignore` | Modify | Add `sbom/` to prevent generated SBOM JSON files from being committed |

---

### Task 1: Add `sbom/` to `.gitignore`

**Files:**
- Modify: `.gitignore`

- [ ] **Step 1: Add the entry**

Open `.gitignore` and append under the `# Generated` section:

```gitignore
# SBOM (generated artifacts, not committed)
sbom/
```

The full updated `# Generated` block should look like:

```gitignore
# Generated
crds/*.yaml
!crds/kustomization.yaml
!crds/kape-handler-webhook.yaml
dashboard/app/types/generated/*.ts

# SBOM (generated artifacts, not committed)
sbom/
```

- [ ] **Step 2: Verify the ignore rule works**

```bash
mkdir -p sbom && touch sbom/test.cdx.json
git status
```

Expected: `sbom/test.cdx.json` does NOT appear in untracked files.

```bash
rm -rf sbom/
```

- [ ] **Step 3: Commit**

```bash
git add .gitignore
git commit -m "chore: ignore sbom/ generated artifacts"
```

---

### Task 2: Create project-level `CLAUDE.md` with PR checklist

**Files:**
- Create: `CLAUDE.md`

- [ ] **Step 1: Create the file**

Create `CLAUDE.md` at the repo root with the following content:

```markdown
# kape-io Project Instructions

## PR Checklist

Before creating any PR, run SBOM generation for all Go modules and post a summary comment:

1. Run `snyk_sbom_scan` MCP tool on each Go module:
   - Path: `./adapters`
   - Path: `./operator`
   - Path: `./task-service`

2. Post a single PR comment via `gh pr comment <PR-URL> --body "<markdown>"` with a markdown table summarising all three SBOMs:

   ```markdown
   ## SBOM Summary

   | Module | Components | Flagged |
   |---|---|---|
   | adapters | <count> | <count or "none"> |
   | operator | <count> | <count or "none"> |
   | task-service | <count> | <count or "none"> |

   Generated via Snyk CycloneDX 1.4 — $(date -u +"%Y-%m-%dT%H:%M:%SZ")
   ```

   If `snyk_sbom_scan` returns no component count, write "N/A".
   If any module scan fails, note the failure in the table row instead of blocking the PR.
```

- [ ] **Step 2: Verify the file is not ignored**

```bash
git status
```

Expected: `CLAUDE.md` appears as an untracked file (not ignored).

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "chore: add project CLAUDE.md with SBOM PR checklist"
```

---

## Verification

After both tasks are complete, confirm the following:

- [ ] `git status` shows a clean tree
- [ ] `cat CLAUDE.md` shows the PR checklist with all three module paths
- [ ] `git check-ignore -v sbom/foo.cdx.json` returns a match (confirms ignore rule is active)
- [ ] `git log --oneline -3` shows both commits
