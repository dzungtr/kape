# GitHub label taxonomy for human- and agent-driven issue triage

## Status

accepted

## Context

The repo tracks all plan/task work as GitHub issues, consumed by both humans and agent rituals (`/standup`, planning, triage, bug intake, state validator). Stock labels gave no machine-readable signal for *where*, *what kind*, or *committed vs not*, so agents fell back to re-reading issue bodies. We need a label set that both audiences can read and write deterministically.

## Decision

Adopt an orthogonal, agent-native label taxonomy: one **Category**, one **Area** (`area/*`), optional **Phase** (`phase/Mx-*`), one mutually-exclusive **Commitment** (`committed`/`stretch`/`backlog`), one mutually-exclusive **Triage state** (`needs-triage`/`needs-info`/`ready-for-agent`/`ready-for-human`/`wontfix`), and additive **Signals** (`urgent`/`blocked`).

The load-bearing decisions, each chosen over a real alternative:

1. **Audience is an explicit, decided state: `ready-for-agent` vs `ready-for-human`.** Triage routes each issue to exactly one. `ready-for-agent` means a hands-off agent may pick it up; `ready-for-human` means the work needs human judgment, external access, design decisions, or manual testing. The split is a deliberate call made in the `/triage` ritual (an agent recommends, a human confirms; the explicit maintainer override applies it directly), defaulting to `ready-for-human` when genuinely ambiguous. *This reverses the original assignee-inference model* — a single `ready` label whose audience was read off the assignee (`ready` + no assignee → agent, `ready` + human assignee → human). That model forced an AFK agent to infer "hands off" from the absence of an assignee, a safety boundary too important to leave implicit. An explicit `ready-for-agent` label makes the agent's pickup query a positive allowlist rather than a fragile inference.

2. **The triage state is a single-label enum, not a derived signal.** `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix` are mutually exclusive; every open issue carries exactly one. The ready states are *decided*, not auto-applied — "does this need a human?" cannot be derived from counting Category/Area/Commitment labels. Those preconditions instead form a **gate**: the state validator reports an ERROR if a ready state is set without one Category, one Area, one Commitment, and no `needs-info`. This adopts the canonical Matt Pocock triage flow directly (`docs/agents/triage-labels.md` is now a 1:1 bridge). The earlier `needs/info` + `needs/repro` + `needs/decision` sub-labels collapse into the single canonical `needs-info`.

3. **Signals are a separate additive axis from the state enum.** An issue can be `ready-for-agent + urgent + blocked` at once — facts the standup agent treats differently. `blocked` does not change the *state* but makes a `ready-for-agent` issue non-pickable (an AFK agent skips `blocked`; the validator WARNs and the issue is re-triaged off `ready-for-agent`). Folding signals onto the state enum would lose this orthogonality.

4. **Priorities anchor to milestones or observable conditions, never to time-decaying words.** `now`/`soon`/`asap`/`later` are forbidden because their meaning rots between agent runs. `urgent` requires an observable trigger (Snyk severity ≥ high, main CI red ≥ 1 day, linked incident) and a `Reason:` line.

5. **State validation runs on-demand, not on a schedule or per-issue event.** The validator runs as a `/standup` step and a manual-dispatch GitHub Action — deliberately no cron and no issues-event trigger, because per-event auto-comments get noisy on every triage edit and a cron adds drift and surprise notifications for a check the maintainer can trigger when it's actually useful.

6. **Labeling is an agent-native Claude skill (`apply-labels`), not an `actions/labeler` GHA.** Claude can read body text, resolve area ambiguity, and reason about Category + Area together — which path-globs cannot — and runs in-session with a human in the loop, avoiding the `pull_request_target` security surface.

## Consequences

The full taxonomy, GitHub colors, mutability table, and the ritual state-machine live in `docs/agent-rituals.md` (the operational contract) and are mapped to the Matt Pocock canonical triage roles in `docs/agents/triage-labels.md`. Changes to the taxonomy require updating those consumer docs in the same PR.

**Revision history:** decisions (1) and (2) were originally written as the inverse — a single derived `ready` label whose audience was inferred from the assignee, with the `needs/*` family split into `needs/info` / `needs/repro` / `needs/decision`. That model was reversed to harden the human-in-the-loop boundary: an AFK agent must read an explicit `ready-for-agent` allowlist, not infer "hands off" from a missing assignee. The change deleted the `ready`, `needs/info`, `needs/repro`, and `needs/decision` labels; added `ready-for-agent`, `ready-for-human`, and `needs-info`; and backfilled all open `ready` issues to `ready-for-human` (re-triaged individually thereafter) and all `needs/*` issues to `needs-info`.
