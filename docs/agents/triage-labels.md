# Triage Labels

The Matt Pocock skills speak in terms of five canonical triage roles. This file maps those roles to the actual label strings used in this repo, which follows the taxonomy defined in [`docs/superpowers/specs/2026-05-16-github-label-system-design.md`](../superpowers/specs/2026-05-16-github-label-system-design.md) and the consumption contracts in [`docs/agent-rituals.md`](../agent-rituals.md).

## Mapping

| Pocock canonical role | Our label    | Meaning                                                |
| --------------------- | ------------ | ------------------------------------------------------ |
| `needs-triage`        | `needs-triage` | Maintainer needs to evaluate this issue              |
| `needs-info`          | `needs/info`   | Waiting on reporter for more information             |
| `ready-for-agent`     | `ready`        | Triage complete, AFK-agent picks up (no assignee)    |
| `ready-for-human`     | `ready`        | Triage complete, human picks up (human assignee)     |
| `wontfix`             | `wontfix`     | Will not be actioned                                  |

When a Pocock skill mentions a canonical role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from the right-hand column.

## Important divergences from Pocock defaults

**Audience is encoded via assignee, not label.** Our taxonomy does not split "ready" into `ready-for-agent` vs `ready-for-human`. Both map to the single `ready` label. To distinguish:

- `ready` + no assignee → an AFK agent can pick it up
- `ready` + human assignee → reserved for a human
- `ready` + agent identifier in assignee → an agent is already working it

If a Pocock skill expects a label-level distinction, infer it from the assignee instead.

**`ready` is a derived label, not directly set.** Per the spec, `ready` is auto-applied by the Triage ritual when an issue has exactly one category, one area, one commitment, optionally one phase, **and** no open `needs/*` labels. It is auto-cleared when those preconditions break.

A Pocock skill that wants to "mark an issue ready" should instead:

1. Ensure the issue has one category (`bug`, `enhancement`, `feature`, etc.)
2. Ensure it has one `area/*` label
3. Ensure it has one commitment label (`committed`, `stretch`, `backlog`)
4. Resolve and remove any open `needs/*` labels

The Triage ritual will then set `ready` automatically.

**Additional `needs/*` sub-labels.** Beyond `needs/info`, our taxonomy includes `needs/repro` (bug needs reproduction steps) and `needs/decision` (awaiting maintainer call). If a Pocock skill detects either condition, apply the more specific sub-label.

## When the mapping is ambiguous

If a Pocock skill action doesn't cleanly fit one of the rows above, prefer the project's taxonomy (`docs/agent-rituals.md`) over Pocock defaults. The skill should adapt to the project, not the other way around.
