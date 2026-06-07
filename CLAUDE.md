# kape-io Project Instructions

## Agent skills

### Issue tracker

Issues and PRDs live as GitHub issues on `dzungtr/kape` (use the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

The kape-io label taxonomy adopts the five canonical Pocock triage roles directly: the triage state is a single-label enum (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`), and the agent-vs-human audience is a decided state set in `/triage`, not inferred from the assignee. See `docs/agents/triage-labels.md`, the decision in `docs/adr/0001-github-label-taxonomy.md`, and the operational contract in `docs/agent-rituals.md`.

### Domain docs

Single-context: `CONTEXT.md` (glossary) and `docs/adr/` (decisions) at the repo root. See `docs/agents/domain.md`.
