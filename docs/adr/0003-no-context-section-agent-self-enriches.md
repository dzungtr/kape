# No `context` section on KapeHandler — the agent self-enriches via MCP tools

## Status

accepted

## Context

Most agent frameworks let the author declare a static `context` block — data pre-fetched and injected into the prompt before the agent runs. A reasonable reader would expect KapeHandler to have one.

## Decision

KapeHandler has **no `context` section**. The agent enriches itself by calling MCP tools during the ReAct loop, fetching exactly the context it needs as it reasons, rather than receiving a pre-assembled static blob.

## Consequences

Context is always fresh and scoped to the actual reasoning path, and there is one mechanism (tool calls) for acquiring information instead of two (static context + tools). The trade-off is more LLM round-trips for data that a static block could have supplied up front, and the engineer expresses *intent* (which tools are available) rather than *wiring* (which data to fetch). Full rationale: `docs/specs/0001-rfc/README.md`.
