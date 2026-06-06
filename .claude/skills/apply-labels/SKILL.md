---
name: apply-labels
description: Apply the kape-io label taxonomy (decision in docs/adr/0001-github-label-taxonomy.md, operational contract in docs/agent-rituals.md) to a GitHub issue or PR. Use when the user asks to label an issue/PR, when a new issue or PR lacks the required dimensions (area/*, category, commitment for issues), or as part of a triage pass. Reads context, derives labels deterministically where possible, and asks the user before applying ambiguous labels. Never sets commitment, urgent, or blocked without an observable trigger and human confirmation.
---

# Apply labels skill

Apply the kape-io label taxonomy to a GitHub issue or PR. The full rule
set lives in `docs/agent-rituals.md`; this skill is the operational
recipe for executing it on one target at a time.

## Inputs

One of:
- A PR number (e.g., "label PR 96") — fetch via `gh pr view <n>`
- An issue number (e.g., "label issue 84") — fetch via `gh issue view <n>`
- No argument — default to the current branch's PR via `gh pr view --json number,title,body,labels,files`

If the user gives a bare number, infer PR vs issue by trying `gh pr view <n>` first; fall back to `gh issue view <n>` if it 404s.

## What to fetch

For a PR:
```
gh pr view <n> --json number,title,body,labels,files,headRefName
```

For an issue:
```
gh issue view <n> --json number,title,body,labels,milestone,assignees
```

## Derivation rules

### Always-safe (apply without asking)

These have observable triggers. Apply directly and report.

**`area/*` from PR changed files** — apply when one area accounts for ≥80% of changed paths.

| If changed files match           | Set                |
|----------------------------------|--------------------|
| `operator/**`                    | `area/operator`    |
| `task-service/**`                | `area/task-service`|
| `adapters/**`                    | `area/adapters`    |
| `kapeproxy/**`                   | `area/kapeproxy`   |
| `runtime/**`                     | `area/runtime`     |
| `dashboard/**`                   | `area/dashboard`   |
| `helm/**`                        | `area/helm`        |
| `crds/**`                        | `area/crds`        |
| `docs/**` (excluding `docs/superpowers/specs/**`), `README.md`, `CONTRIBUTING.md` | `area/docs` |
| `.github/**`, `tools/**`         | `area/ci`          |

**`area/*` from issue title** — apply when exactly one keyword bucket matches the title (case-insensitive).

| Title contains                                          | Set                |
|---------------------------------------------------------|--------------------|
| operator, reconciler, KapeHandler, KapeTool, KapeSchema, CRD validation | `area/operator` |
| task-service, task service, OpenAPI                     | `area/task-service`|
| adapter, AlertManager, audit adapter                    | `area/adapters`    |
| kapeproxy, MCP proxy                                    | `area/kapeproxy`   |
| runtime, LangGraph, Python, handler routes              | `area/runtime`     |
| dashboard, SSE, EventSource, OAuth2 Proxy               | `area/dashboard`   |
| helm, chart, template                                   | `area/helm`        |
| CRD, CEL, XValidation                                   | `area/crds`        |
| docs, README, runbook, CHANGELOG                        | `area/docs`        |
| CI, workflow, Actions, Snyk                             | `area/ci`          |
| NATS, Postgres, CloudNativePG, cert-manager, ESO, External Secrets | `area/infra` |

**Category from PR paths** — apply when all changed files match one category bucket.

| If all changed files are        | Set      |
|---------------------------------|----------|
| `docs/superpowers/specs/**`     | `spec`   |
| under a `docs/` tree, no source | `docs`   |
| test files (`*_test.go`, `test_*.py`, `*.test.ts`, `tests/**`) | `test` |

**`phase/Mx-*` from issue title** — apply when title matches `[Pn/...]`.

| Prefix       | Set                  |
|--------------|----------------------|
| `[P6/...]`   | `phase/M2-operator`  |
| `[P7/...]`   | `phase/M3-runtime`   |
| `[P8/...]`   | `phase/M4-security`  |
| `[P9/...]`   | `phase/M5-dashboard` |
| `[P10/...]`  | `phase/M6-release`   |

### Ask-before-applying

These require judgment. Propose and confirm.

- **Ambiguous area** — when multiple area buckets match a PR's files (e.g., a PR touching both `operator/` and `crds/`). List the matches, recommend the dominant one, ask.
- **Category** — for issues (which have no file paths) and PRs that span code. Look at title/body, propose one of `bug`, `enhancement`, `feature`, `refactor`, `redesign`, `security`, `chore`. Confirm.
- **`needs-triage` / `ready`** — for issues. If category + area + commitment all present and no open `needs/*`, propose `ready`. Otherwise propose `needs-triage`.

### Never apply autonomously

The spec defines these labels as agent-only-with-cause. Always require the user to set them explicitly.

- **`committed` / `stretch` / `backlog`** — commitment is a planning decision. Suggest based on milestone presence (issue in milestone → suggest `committed`; no milestone → suggest `backlog`), but require user confirmation.
- **`urgent`** — requires "Reason:" line in body and a triggering condition (Snyk severity ≥ high, broken main CI, linked incident). Never apply unless the user explicitly asks.
- **`blocked`** — requires "Blocked by: <ref>" line. Never apply without confirmation.
- **`good first issue` / `help wanted`** — maintainer signal, never auto-applied.

## Process

1. **Resolve the target.** Determine PR vs issue from input.
2. **Fetch context.** Read JSON above; print title and current labels in one short line.
3. **Derive.** Walk the always-safe rules; collect proposed adds. Walk the ask-before-applying rules; collect proposed adds that need confirmation.
4. **Diff.** Compute `to_add = derived - existing`. Skip any label already present.
5. **Confirm ambiguous adds.** Present a single message: "I'll add [auto-derived list]. For [ambiguous categories], I propose [X] — confirm or override?" Wait for the user.
6. **Apply.** `gh issue edit <n> --add-label "<csv>"` or `gh pr edit <n> --add-label "<csv>"`.
7. **Report.** One line summary: `Labeled #<n>: added [<list>]. Existing: [<unchanged list>].`

## Edge cases

- **No `area/*` derivable** — for PRs, list the changed paths and ask the user which area. For issues, fall through to `needs-triage`.
- **PR already has labels** — only add missing ones. Never remove labels unless the user asks.
- **Target is closed or merged** — refuse with a one-line message. Labeling closed work has no consumer.
- **`gh` not authenticated** — print the auth command and stop.

## Reference

Full rules and the rationale for each label live in:
- `docs/adr/0001-github-label-taxonomy.md` (the taxonomy decision and rationale)
- `docs/agent-rituals.md` (rituals — read before adding new label-application logic)

Update both files if rules change, then update this skill in lockstep.
