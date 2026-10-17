# Host consumption: blocking `wait_agent` long-poll, wait-first loop

## Status

accepted

## Context

Polling `read_events` by cursor made the host model burn an inference round per poll over its whole context, flooding it with raw records mid-turn (spec #230). ADR-0083 already established the long-poll shape in one direction — the controller long-polls the passive daemon, and the poll is the heartbeat.

## Decision

Mirror ADR-0083 outward: the host blocks on `wait_agent(id, timeout)` (REST `GET /agents/{id}/wait`, default 300s, server cap 600s; MCP tool of the same name), and the controller holds the connection until something worth deciding happens — an FSM state change, a **verdict transition** (a repeated verdict is not a wake), a liveness failure, or timeout. The single coalesced response is a minimal snapshot `{agentId, changed, state, assessment}`; raw records never cross this surface. The host's canonical loop becomes: `prompt_agent` → `get_agent` (orientation) → `wait_agent` (re-call on timeout) → verdict wake: route by single responsibility — `looping` → the host first retrieves the recent transcript slice via a bounded `read_events` pull and judges steer vs abort itself (the verdict triggers the pull; the host owns the judgment); `blocked` → fix the environment and revive, never re-prompt unchanged; `degraded` → halt new dispatches and diagnose the platform → idle wake: terminal summary, then one tail `read_events` pull only if deliverables are missing from the summary. While blocked the host runs zero inferences; cursor-based `read_events` demotes from monitoring mechanism to two bounded uses: looping-branch judgment pull and terminal tail pull.

## Consequences

A healthy 30-minute turn costs the host ~2 inference rounds (prompt + settle wake) instead of ~60 polling rounds. Terminal summaries become load-bearing for output consumption, so their prompts must preserve deliverable references (PR numbers, URLs). The host MCP client's tool-timeout must exceed the requested wait timeout or the client aborts early — documented in the dispatch skill (plugins/kape), whose consumption section this decision rewrites. The surface reuses the in-process SSE hub with a wake filter; no new transport machinery. Spec: #230.
