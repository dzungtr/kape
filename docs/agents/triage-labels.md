# Triage Labels

The Matt Pocock skills speak in terms of five canonical triage roles. This repo adopts those roles **directly** — the label strings match the canonical names one-to-one. The taxonomy and the reasoning behind it are recorded in [ADR-0001 — GitHub label taxonomy](../adr/0001-github-label-taxonomy.md); the live operational contract (full label set, state machine, mutability table) is [`docs/agent-rituals.md`](../agent-rituals.md).

## Mapping

| Pocock canonical role | Our label         | Meaning                                                       |
| --------------------- | ----------------- | ------------------------------------------------------------- |
| `needs-triage`        | `needs-triage`    | Maintainer needs to evaluate this issue                       |
| `needs-info`          | `needs-info`      | Blocked awaiting reporter or maintainer input                 |
| `ready-for-agent`     | `ready-for-agent` | Fully specified; an AFK agent may pick it up                  |
| `ready-for-human`     | `ready-for-human` | Triaged but needs human implementation                        |
| `wontfix`             | `wontfix`         | Will not be actioned (issue is closed)                        |

A Pocock skill's role name and our label string are identical — apply it as written.

## The state axis is a single-label enum

These five labels form the **triage state**: every open issue carries **exactly one** of them. They are mutually exclusive — an issue is never both `needs-triage` and `ready-for-agent`, never both `ready-for-agent` and `ready-for-human`. The [state validator](../agent-rituals.md#state-validator) reports an ERROR on any open issue with zero or more than one state label.

State transitions follow the canonical Pocock flow: a new issue enters `needs-triage`; from there it moves to `needs-info`, `ready-for-agent`, `ready-for-human`, or `wontfix`. `needs-info` returns to `needs-triage` once the blocker clears.

## Important divergences from Pocock defaults

**The audience split is decided, not derived.** `ready-for-agent` vs `ready-for-human` is a deliberate judgment call made during the `/triage` ritual, not a projection of other labels. An issue is `ready-for-human` when the work needs human judgment, external access, design decisions, or manual testing; otherwise it is `ready-for-agent`. When the call is genuinely ambiguous, default to `ready-for-human` — wrongly handing human-judgment work to an AFK agent is costlier than the small latency of routing agent-suitable work to a human. See [ADR-0001 decision 1](../adr/0001-github-label-taxonomy.md) for the reasoning behind reversing the older assignee-inference model.

**A precondition gate guards the ready states.** Before an issue may move to `ready-for-agent` or `ready-for-human` it must have exactly one category (`bug`, `enhancement`), one `area/*`, and one commitment label (`committed`, `stretch`, `backlog`), and carry no `needs-info`. This gate is enforced by the [state validator](../agent-rituals.md#state-validator), which reports an ERROR when a ready state is set without the preconditions. A skill that wants to "mark an issue ready" should first satisfy the gate, then choose the audience.

**`blocked` is an orthogonal signal, not a state.** `blocked` (and `urgent`) sit on a separate additive axis and may co-exist with any state. A `ready-for-agent` issue that becomes `blocked` is **not pickable** — the validator WARNs and the issue must be re-triaged off `ready-for-agent`. AFK-agent pickup is therefore `ready-for-agent` AND NOT `blocked` AND no assignee.

## When the mapping is ambiguous

If a Pocock skill action doesn't cleanly fit one of the rows above, prefer the project's taxonomy (`docs/agent-rituals.md`) over Pocock defaults. The skill should adapt to the project, not the other way around.
