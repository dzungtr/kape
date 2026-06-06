# `event-publish` tools live in `actions[]` only, with engineer-controlled routing

## Status

accepted

## Context

A KapeHandler can use `event-publish` KapeTools to emit downstream events. These could be exposed to the LLM as ordinary tools it calls mid-reasoning, or constrained to a dedicated post-reasoning stage. Letting the LLM publish events freely during the ReAct loop mixes the agent's investigation with its side effects.

## Decision

`event-publish` tools live in the handler's `actions[]` section **only** — never as general tools in the ReAct loop. The LLM fills `$prompt` content fields (the event payload it reasons into), while the engineer controls *routing* — which events fire under which conditions — declaratively via `actions[]` conditions.

## Consequences

Side effects are separated from investigation: the agent reasons, then the engineer's declared conditions decide what is published, keeping routing auditable and out of the LLM's discretion. The LLM cannot emit an event the engineer didn't wire a path for. The cost is less LLM autonomy over downstream effects — deliberate, since routing is intent the engineer owns, not reasoning the agent owns. Full rationale: `docs/specs/0001-rfc/README.md` and `docs/specs/0002-crds-design/README.md`.
