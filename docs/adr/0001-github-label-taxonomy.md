# GitHub label taxonomy for human- and agent-driven issue triage

## Status

accepted

## Context

The repo tracks all plan/task work as GitHub issues, consumed by both humans and agent rituals (`/standup`, planning, triage, bug intake, state validator). Stock labels gave no machine-readable signal for *where*, *what kind*, or *committed vs not*, so agents fell back to re-reading issue bodies. We need a label set that both audiences can read and write deterministically.

## Decision

Adopt an orthogonal, agent-native label taxonomy (~39 labels): one **Category**, one **Area** (`area/*`), optional **Phase** (`phase/Mx-*`), one mutually-exclusive **Commitment** (`committed`/`stretch`/`backlog`), additive **Signals** (`urgent`/`blocked`/`ready`/`needs-triage`), and additive **Triage needs** (`needs/*`).

The load-bearing decisions, each chosen over a real alternative:

1. **Audience is encoded via assignee, not a label.** We do *not* split readiness into `ready-for-agent` / `ready-for-human`. A single derived `ready` label plus the assignee carries it: `ready` + no assignee → an AFK agent may pick it up; `ready` + human assignee → reserved for a human; `ready` + agent-id assignee → an agent is in-flight. This avoids a second label that would have to be kept in sync with the assignee.

2. **`ready` is derived, never set directly.** Triage auto-applies `ready` exactly when an issue has one Category, one Area, one Commitment, optionally one Phase, and no open `needs/*`; it auto-clears when any precondition breaks. `ready` is a projection of state, not an independent toggle, so it can't drift.

3. **Signals are a separate additive axis from Commitment.** An issue can be `committed + urgent + blocked` at once — three facts the standup agent treats differently. Folding them onto one ordinal priority axis would lose that.

4. **Priorities anchor to milestones or observable conditions, never to time-decaying words.** `now`/`soon`/`asap`/`later` are forbidden because their meaning rots between agent runs. `urgent` requires an observable trigger (Snyk severity ≥ high, main CI red ≥ 1 day, linked incident) and a `Reason:` line.

5. **State validation runs on-demand, not on a schedule or per-issue event.** The validator runs as a `/standup` step and a manual-dispatch GitHub Action — deliberately no cron and no issues-event trigger, because per-event auto-comments get noisy on every triage edit and a cron adds drift and surprise notifications for a check the maintainer can trigger when it's actually useful.

6. **Labeling is an agent-native Claude skill (`apply-labels`), not an `actions/labeler` GHA.** Claude can read body text, resolve area ambiguity, and reason about Category + Area together — which path-globs cannot — and runs in-session with a human in the loop, avoiding the `pull_request_target` security surface.

## Consequences

The full taxonomy, GitHub colors, mutability table, and the ritual state-machine live in `docs/agent-rituals.md` (the operational contract) and are mapped to the Matt Pocock canonical triage roles in `docs/agents/triage-labels.md`. Changes to the taxonomy require updating those consumer docs in the same PR.

**Open / under reconsideration:** decision (1) — the single derived `ready` + assignee model — is being revisited in a future session that may adopt distinct `ready-for-agent` / `ready-for-human` states to harden the human-in-the-loop boundary. This ADR records the model as it stands today; a follow-up ADR will supersede decision (1) if that redesign lands.
