# GitHub Label System Design

**Date:** 2026-05-16
**Status:** Draft for review
**Scope:** Repository-wide label taxonomy for `dzungtr/kape`, designed to be readable and writable by both humans and agents (`/standup`, planning, triage).

## Problem

The repository currently has only stock GitHub labels plus `roadmap-sync` and `snyk-finding`. Every roadmap issue carries the same `roadmap-sync` label and most PRs are unlabeled. As a result:

- Issues cannot be filtered by *where* (which subsystem), *what kind of work*, or *whether the project has committed to do them*.
- Agent rituals (`/standup`, planning, bug intake) have no machine-readable signal to rank work — they fall back to re-reading issue bodies.
- Newly filed bugs, security findings, and roadmap slices all look the same in queries.

The label system must serve both humans browsing the issue tracker and agents that programmatically consume label state to decide what to do next.

## Design principles

1. **Orthogonal dimensions.** Each label answers one question. Filters compose.
2. **Agent contracts, not human hints.** Every label in the priority/signal block has a documented rule for when an agent may set it and what consuming agents do with it. The label's GitHub `description` field is part of the contract.
3. **No time-decaying vocabulary.** Forbidden: `now`, `soon`, `asap`, `later` (as priorities). Anchor to milestones or to observable conditions.
4. **Observable triggers preferred.** Where possible, an agent can apply a label from a checkable condition (Snyk severity, failed CI, milestone capacity) rather than a human typing it.
5. **Mutually-exclusive sets are explicit.** So agents can validate state and surface conflicts.
6. **Small surface.** ~40 labels total. Every label earns its slot.

## Dimensions overview

| Dimension     | Prefix       | Cardinality per issue | Required for `ready` |
|---------------|--------------|-----------------------|----------------------|
| Category      | flat         | 1                     | yes                  |
| Area          | `area/*`     | 1 (usually)           | yes                  |
| Phase         | `phase/*`    | 0–1                   | yes if roadmap-tracked |
| Commitment    | flat         | 1 (mutually exclusive)| yes                  |
| Signals       | flat         | 0–n                   | no                   |
| Triage needs  | `needs/*`    | 0–n                   | no                   |
| Standalone    | flat         | 0–n                   | no                   |

An issue is **`ready`** when it has exactly one Category, one Area, one Commitment, optionally one Phase, and no open `needs/*` labels.

## Label set

### Category — flat, exactly one per issue

```
bug              #d73a4a   Defect — something is broken
enhancement      #a2eeef   Improve an existing capability
feature          #84b6eb   New capability that doesn't exist yet
refactor         #fbca04   Internal restructure, no behaviour change
redesign         #ff9f1c   Reshape architecture/UX/API — behaviour changes
security         #b60205   Security-relevant change (vuln fix, hardening, policy)
docs             #0075ca   Documentation-only
chore            #ededed   Deps, config, build, tooling
test             #c5def5   Test-only changes
spec             #5319e7   Design or spec doc under docs/superpowers/specs/
```

Notes:
- `enhancement` modifies a thing that exists; `feature` adds a thing that doesn't.
- `refactor` is invisible to users; `redesign` is visible.
- `security` is set independently of `bug` (a security `refactor` is valid).

### Area — `area/*`, one per issue

Green (`#0e8a16`) family, one per area of the codebase.

```
area/operator           Go operator (CRDs, reconcilers)
area/task-service       Go task-service
area/adapters           Go event adapters (alertmanager, k8s-audit, …)
area/kapeproxy          Go MCP proxy
area/runtime            Python LangGraph runtime
area/dashboard          TypeScript dashboard
area/helm               Helm charts + manifests
area/crds               CRD schemas + CEL validation
area/docs               docs/, README, runbooks (excludes spec docs — see category=spec)
area/ci                 .github/workflows, tooling
area/infra              NATS, Postgres, cert-manager, External Secrets Operator
```

### Phase — `phase/Mx-*`, one per roadmap-tracked issue

Purple (`#5319e7`).

```
phase/M2-operator       Phase 6 — Full Operator
phase/M3-runtime        Phase 7 — Full Runtime
phase/M4-security       Phase 8 — K8s Audit + Security
phase/M5-dashboard      Phase 9 — Dashboard
phase/M6-release        Phase 10 — Helm + Examples + Polish
```

The `phase/*` label mirrors the issue's milestone. It exists separately because:
1. Agents can filter by phase without an extra milestone API call.
2. Survives milestone renames or splits.
3. Lets non-milestone issues (drive-by bugs filed against a phase area) be associated without consuming a milestone slot.

### Commitment — flat, mutually exclusive (exactly one on a `ready` issue)

```
committed    Belongs to its milestone. Counts against milestone capacity. Slipping = explicit decision.
stretch      Targeted for its milestone if capacity allows. First to slip in a scope cut.
backlog      Not committed to any milestone. Not counted by planning. Re-evaluated each cycle.
```

Agent contract: planning ritual must validate `sum(effort of committed in M) ≤ capacity(M)`. An issue cannot carry more than one of these labels; a state validator flags violations.

### Signals — flat, additive (0–n per issue)

```
urgent        Interrupt rule. Standup ranks above committed work. Issue body MUST contain "Reason: <one line>". Agents MAY set from: { Snyk severity ≥ high, main-branch CI red ≥ 1 day, linked production incident }.
blocked       Cannot progress. Issue body MUST contain "Blocked by: <#issue|PR|external>". Agents MAY set from: { linked PR has merge conflicts ≥ 3 days, depended-on issue still open past commitment date }.
ready         Triage complete: has category + area + commitment, no open needs/*. Planning agents pick from this pool. Agents MUST set when those conditions hold; MUST clear when they stop holding.
needs-triage  Inverse of ready. Default on every new issue. Agents MUST set on any issue missing category or area.
```

Why signals are separate from commitment: an issue can be `committed + urgent + blocked` simultaneously — that combination tells the standup agent something different (committed work that has interrupt priority but is also stuck). Folding all three into one ordinal axis would lose that.

### Triage needs — `needs/*`, additive (0–n)

```
needs/repro       Bug needs reproduction steps from reporter.
needs/decision    Awaiting a maintainer call on scope or approach.
needs/info        Awaiting reporter or requester response.
```

Presence of any `needs/*` label prevents `ready`.

### Standalone keepers

```
good first issue   #7057ff   (stock)
help wanted        #008672   (stock)
snyk-finding       #d73a4a   Already wired into /standup datasources.
```

### Retired

| Label          | Replaced by                                   |
|----------------|-----------------------------------------------|
| `roadmap-sync` | `phase/Mx-*` + category (after backfill)      |
| `invalid`      | Close with reason                             |
| `duplicate`    | Close with reason + linked issue              |
| `wontfix`      | Close with reason                             |
| `question`     | Convert to Discussion or close                |

`roadmap-sync` is retained through the backfill transition; remove after the migration script is updated.

## Folded-away dimensions

The following were considered and rejected in favour of observable state:

| Considered                | Why not                                                          |
|---------------------------|------------------------------------------------------------------|
| `status/in-progress`      | Observable via assignee or linked open PR.                       |
| `status/in-review`        | Observable via linked open PR.                                   |
| `status/needs-rebase`     | PR-flow only; PRs use mergeable state, not labels.               |
| `status/do-not-merge`     | PR-flow only.                                                    |
| `status/accepted`         | Equivalent to `ready` signal.                                    |
| `priority/P0…P3`          | Incident-severity vocabulary; doesn't fit issue planning flow.   |
| `priority/now/next/later` | Time-decaying — meaning rots between runs of an agent.           |
| Release labels            | Milestones already represent releases.                           |
| `lifecycle/stale`         | Premature — defer until backlog actually goes stale.             |
| Per-author / per-LLM / per-customer labels | Not in use today; premature.                    |

## Automation hooks

Each hook is a small, well-scoped change. Detail goes in the implementation plan; this spec only fixes the surface.

1. **PR auto-labeler.** `.github/labeler.yml` mapping path globs → `area/*` and category for unambiguous paths (`docs/**` → `docs`, `**/*_test.go` → `test`, `helm/**` → `area/helm`).
2. **Snyk integration.** When `snyk-finding` is set (existing flow), also set `security` + `urgent`. Set `committed` only if a maintainer assigns the issue to a milestone; otherwise leave `backlog`.
3. **`/standup` extension.** Add datasources in `.claude/standup.json` for open issues with `urgent` and `blocked` so the daily report buckets them explicitly. The existing `bug` datasource is kept.
4. **`needs-rebase` automation for PRs only.** Out of scope for this label system — PRs use GitHub's mergeable state directly.
5. **Roadmap migration script update.** `tools/migrate-to-github.sh` sets `enhancement` + `area/*` (derived from title keywords) + `phase/Mx-*` instead of bare `roadmap-sync`.

## Backfill

Open issues only — closed issues left alone.

Script (in plan, not now):
1. Parse `[Pn/mm]` from title → derive `phase/Mx-*` via phase→milestone table.
2. Keyword-map title fragments → `area/*` (e.g., "operator" → `area/operator`, "OAuth2 Proxy" → `area/helm`).
3. Default category = `enhancement` for roadmap items; `bug` if existing labels include `bug`.
4. Default commitment = `committed` if the milestone is open and the issue is in it; `backlog` otherwise.
5. Default signal = `needs-triage` if category or area cannot be derived unambiguously; `ready` otherwise.
6. Dry-run prints proposed mutations; apply only with `--confirm`.

PRs: only relabel currently-open PRs (~3 today) — same script with `--targets prs`.

## Documented agent rituals

A companion doc, `docs/agent-rituals.md`, codifies how agents consume the labels. Summarised here; full text lives in that doc and ships with this PR.

### `/standup` consumption order

```
1. urgent + open                                    → "Act now" bucket
2. blocked + open + age_days(blocked) > 3           → "Stuck" bucket
3. committed + (assignee OR linked open PR)         → "In flight" bucket
4. committed + ready + no assignee                  → "Pick next" suggestion
5. stretch + ready + assignee                       → "Progress check" bucket
   everything else: not shown
```

### Milestone-planning ritual

```
At start of milestone M (run by planning agent or human + agent):
  capacity_M = configured slot count for M
  if count(committed where milestone = M) > capacity_M:
    suggest demoting newest committed → stretch
  if count(committed where milestone = M) < capacity_M:
    promote highest-value stretch → committed until full
  any issue in milestone M without a commitment label:
    set needs-triage, post comment requesting decision
```

### Triage ritual

```
On new issue:
  set needs-triage
  if body contains security keywords ("CVE", "vuln", "secret leak", "RCE", "auth bypass"):
    set security + urgent
  derive area/* from title keywords; if unambiguous, set it
  default commitment = backlog
  do NOT set ready

On issue update or label change:
  if has category + area + commitment AND no open needs/* AND not needs-triage:
    set ready, clear needs-triage
  if loses any of those preconditions:
    clear ready
```

### Bug intake (specific case)

```
On Snyk MCP finding (existing flow):
  set bug + security + snyk-finding + urgent + area/<derived>
  default commitment = backlog (maintainer promotes to committed by milestoning)
```

### State-validator ritual

A periodic agent (or pre-commit-style check) flags inconsistent label state:

```
issue carries > 1 commitment label                          → error
issue is ready but missing category, area, or commitment    → error
issue is ready and has any needs/* label                    → error
issue carries urgent without "Reason:" in body              → warn
issue carries blocked without "Blocked by:" in body         → warn
```

## Surface count

| Block                    | Count |
|--------------------------|-------|
| Category                 | 10    |
| Area                     | 11    |
| Phase                    | 5     |
| Commitment               | 3     |
| Signals                  | 4     |
| Triage needs             | 3     |
| Standalone keepers       | 3     |
| **Total**                | **39**|

(Plus 1 transitional: `roadmap-sync`, removed after backfill.)

## Open questions deferred to the implementation plan

- Exact title-keyword → `area/*` mapping table for the backfill script.
- `area/*` for issues that span two areas — pick the dominant one, or allow two? (Currently: pick dominant, document in body.)
- Whether the state-validator runs as a GitHub Action, a `/standup` step, or both.
- Slot count per milestone for the planning capacity check.

## Success criteria

- Any open issue, viewed cold, answers *where*, *what kind*, and *committed vs not* from labels alone.
- `/standup` can rank work without re-reading issue bodies.
- A new contributor can filter `area/runtime + good first issue` and find onboarding work.
- A planning agent can compute milestone capacity vs commitment from a single label query.
- A triage agent can move an issue from new → `ready` autonomously when conditions hold.

## Out of scope

- Implementing the labels themselves, the auto-labeler workflow, the Snyk-handler change, the migration script update, the state validator, or the rituals doc itself. Each lands as a step in the implementation plan that follows this spec.
- Per-author, per-LLM, per-customer, or release-version labels.
- Discussion or Project (board) configuration.
