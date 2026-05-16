# Agent Rituals — Label Consumption Contracts

This document is the consumer-side contract for the label taxonomy defined in
[`docs/superpowers/specs/2026-05-16-github-label-system-design.md`](superpowers/specs/2026-05-16-github-label-system-design.md).

Every agent ritual that reads or writes labels MUST follow the rules here. The
intent is that a new agent or maintainer can read this doc and behave
consistently with the existing automation without re-deriving rules from issue
bodies.

## Roles

| Role               | Reads labels                              | Writes labels                          |
|--------------------|-------------------------------------------|----------------------------------------|
| `/standup`         | all                                       | none                                   |
| Planning           | `committed`, `stretch`, `backlog`, `ready`, `phase/*` | `committed` ⇄ `stretch`, `needs-triage` |
| Triage             | `needs/*`, category, area, commitment     | category, area, commitment, `ready`, `needs-triage` |
| Bug intake         | (Snyk MCP output, new-issue events)       | `bug`, `security`, `urgent`, `snyk-finding`, `area/*`, `backlog` |
| `apply-labels` skill | target issue/PR labels, body, title, changed files | `area/*`, category, `phase/*`; proposes commitment but never applies without user confirmation |
| State validator    | all                                       | none (reports only)                    |

Only the writer roles above may set the listed labels. Other agents read but do
not write.

## `/standup` consumption order

The `/standup` skill reads open issues and ranks them into five buckets. Order
matters — an issue surfaces in the first bucket it matches and not subsequent
ones.

```
1. ACT NOW         urgent + open
2. STUCK           blocked + open + age_days(blocked) > 3
3. IN FLIGHT       committed + (assignee OR linked open PR)
4. PICK NEXT       committed + ready + no assignee
5. PROGRESS CHECK  stretch + ready + assignee
   (anything not matched: not shown)
```

`age_days(blocked)` is computed from the timestamp at which the `blocked` label
was last applied. `/standup` retrieves this from the issue events API and
caches it for the run.

### Bucket display

Each bucket shows: issue number, title, area, commitment, age (in bucket), and
any `needs/*` labels. `urgent` issues additionally show the "Reason:" line from
the body verbatim.

## Planning ritual

Run at the start of each milestone, mid-milestone if scope changes, and at the
end to settle the next milestone.

```
At start of milestone M:
  capacity_M = configured slot count for M  (see configuration below)

  if count(committed where milestone = M) > capacity_M:
    suggest demoting newest committed → stretch
    (do not mutate without maintainer confirmation)

  if count(committed where milestone = M) < capacity_M:
    promote highest-value stretch → committed until full
    (highest-value = oldest non-stale by default)

  for each issue in milestone M without a commitment label:
    set needs-triage
    post comment requesting decision
```

### Capacity configuration

Slot counts per milestone live in `.claude/standup.json` alongside existing
datasources:

```json
{
  "milestones": {
    "M2": { "slots": 8 },
    "M3": { "slots": 10 },
    "M4": { "slots": 6 },
    "M5": { "slots": 7 },
    "M6": { "slots": 5 }
  }
}
```

If a milestone has no slot count, the planning agent reports the count and asks
the maintainer to set it before mutating any labels.

## Triage ritual

Runs on every new issue and on every issue label change.

```
On new issue:
  set needs-triage
  if body contains security keywords (CVE, vuln, secret leak, RCE, auth bypass):
    set security + urgent
  derive area/* from title keywords (see mapping below); if unambiguous, set it
  default commitment = backlog
  do NOT set ready (triage is not complete until a maintainer or agent confirms category)

On issue update or label change:
  preconditions =
    has exactly one category label
    AND has exactly one area/* label
    AND has exactly one commitment label
    AND has no needs/* labels
    AND is not labelled needs-triage

  if preconditions hold:
    set ready, clear needs-triage
  else:
    clear ready (do not touch needs-triage — that is set by triage logic above)
```

### Title-keyword → `area/*` mapping (initial)

| If title contains (case-insensitive)        | Set                |
|---------------------------------------------|--------------------|
| `operator`, `reconciler`, `KapeHandler`, `KapeTool`, `KapeProxy`, `KapeSchema`, `CRD validation` | `area/operator` |
| `task-service`, `task service`, `OpenAPI`   | `area/task-service`|
| `adapter`, `AlertManager`, `audit adapter`  | `area/adapters`    |
| `kapeproxy`, `MCP proxy`                    | `area/kapeproxy`   |
| `runtime`, `LangGraph`, `Python`, `handler routes` (Python context) | `area/runtime` |
| `dashboard`, `SSE`, `EventSource`, `OAuth2 Proxy` | `area/dashboard` |
| `helm`, `chart`, `template`                 | `area/helm`        |
| `CRD`, `CEL`, `XValidation`                 | `area/crds`        |
| `docs`, `README`, `runbook`, `CHANGELOG`    | `area/docs`        |
| `CI`, `workflow`, `Actions`, `Snyk`         | `area/ci`          |
| `NATS`, `Postgres`, `CloudNativePG`, `cert-manager`, `ESO`, `External Secrets` | `area/infra` |

If two areas match, pick the most specific (e.g., `KapeProxy` in
`area/operator` over `MCP proxy` in `area/kapeproxy`) and document the decision
in a triage comment.

## Phase sync

`phase/Mx-*` mirrors the issue's milestone. The rule:

```
On milestone change (assignment, reassignment, removal):
  set phase/Mx-* matching the new milestone (or clear if milestone removed)
  if milestone has no corresponding phase/* (e.g., M1 closed), do not set

On phase/* label change without a matching milestone:
  state validator reports WARN "phase/* without matching milestone"
```

The state validator (below) also flags drift between milestone and `phase/*`.

## Bug intake (Snyk MCP)

Triggered by an `snyk_code_scan` finding that does not yet have an open issue.

```
On Snyk MCP finding:
  open issue if no issue exists for finding ID
  set bug + security + snyk-finding + urgent
  derive area/* from scan path (./operator → area/operator, ./adapters → area/adapters, ./task-service → area/task-service)
  default commitment = backlog
  add "Reason: Snyk <severity> finding — <CWE id>" to body for urgent contract
  do NOT assign milestone (maintainer promotes to committed by milestoning)
```

## State validator

A periodic agent (see "Where it runs" below) checks open issues for inconsistent
label state and reports violations. The validator never mutates labels — it
only reports.

```
For each open issue:
  if count(commitment labels) > 1:                          report ERROR "multiple commitments"
  if ready AND missing category:                            report ERROR "ready without category"
  if ready AND missing area:                                report ERROR "ready without area"
  if ready AND missing commitment:                          report ERROR "ready without commitment"
  if ready AND any needs/* label:                           report ERROR "ready with open needs/*"
  if urgent AND body has no "Reason:" line:                 report WARN  "urgent without reason"
  if blocked AND body has no "Blocked by:" line:            report WARN  "blocked without reference"
  if needs-triage AND ready:                                report ERROR "both needs-triage and ready"
  if has phase/* AND no milestone:                          report WARN  "phase/* without matching milestone"
  if has milestone AND no phase/*:                          report WARN  "milestone without phase/*"
```

### Where it runs

The state validator runs in two places:
1. As a step in the `/standup` skill, before the bucket ranking, so violations
   surface in the daily report.
2. As a scheduled GitHub Action (weekly), to catch issues that `/standup` did
   not run against.

Both share the same rule set; the GitHub Action posts a comment on each
offending issue with the violation list.

## Mutability summary

| Label                   | Settable by                                   | Cleared by                                |
|-------------------------|-----------------------------------------------|-------------------------------------------|
| Category labels         | Triage, bug intake, `apply-labels` skill, human | Human only                              |
| `area/*`                | Triage, bug intake, `apply-labels` skill, human | Human only                              |
| `phase/*`               | Phase-sync (on milestone change), migration script, `apply-labels` skill, human | Phase-sync, human |
| `committed`             | Planning, human (by milestone assignment)     | Planning (→ stretch), human               |
| `stretch`               | Planning, human                               | Planning (→ committed), human             |
| `backlog`               | Triage, bug intake, human                     | Planning (→ stretch / committed), human   |
| `urgent`                | Triage (security only), bug intake, human     | Human (or auto-clear when issue closes)   |
| `blocked`               | Triage, human, automation (PR conflict)       | Triage, human, automation                 |
| `ready`                 | Triage (auto from preconditions)              | Triage (auto when preconditions break)    |
| `needs-triage`          | Triage (default on new), planning             | Triage (auto when ready set)              |
| `needs/*`               | Triage, human                                 | Triage, human                             |
| `snyk-finding`          | Bug intake                                    | Human only                                |
| `good first issue`      | Human                                         | Human                                     |
| `help wanted`           | Human                                         | Human                                     |

"Human" includes a human operating through the CLI with full intent — i.e., not
an autonomous agent. Agents may suggest changes that would require a "human"
mutation, but must not apply them.

## Evolution

Label additions, removals, or rule changes require a PR that updates both this
file and `docs/superpowers/specs/2026-05-16-github-label-system-design.md`.
Agent prompts and skill definitions reference both files; keeping them in sync
prevents agents and humans drifting apart.
