---
name: dispatch
description: USE FOR dispatching work to kape-controller sandbox agents — creating, prompting, waiting on, or deleting agents via the kape-controller MCP server; debugging sandbox-agent children; or any task mentioning kape-controller. Covers dispatch lifecycle, error classification, and output consumption.
---

# Dispatching work to kape-controller sandbox agents

## 1. Consume the surface, don't assume it

Do enumerate the kape-controller tools via the MCP gateway (server status or tool
listing) before first use in a session. Do read each tool's description and input
schema from the server itself — the live server is its own documentation.

Never hardcode argument shapes or assume a field exists. The tool contract drifts
with the controller's code: the `provider` field was removed from `create_agent`
after a live incident, and the default is now deployment-fixed. Do pass exactly the
inputs the current schema asks for — nothing more, nothing less.

Do this every session: do not rely on remembered tool shapes from a previous
session or from this document.

## 2. Error classification (hard rules)

Never guess config values. One validation error (e.g. "provider not found",
FailedPrecondition) means a config or registry problem — do report it to the
operator. Do NOT retry with a different guessed value. Never cycle candidate
values in a retry storm; a real session produced 8 failed `create_agent` calls in
36 seconds cycling guessed providers.

Do check the error class before drawing conclusions. A validation error from one
call does not mean the server is down or "unusable" — a healthy list/status call
alongside a failing create proves the server is up and the problem is
configuration. Never declare the server unusable off a single failing tool call.

## 3. Dispatch lifecycle

1. Do list existing agents first — never assume an empty controller.
2. Do create the agent with the resources and model the task needs; config
   defaults come from the deployment, do not override them by guessing.
3. Do prompt the agent. Prompt acceptance is fire-and-forget: the call returns
   once the turn is accepted, not when the work finishes. A prompt against a turn
   already in flight is rejected — do wait for that turn to settle, never hammer.
4. Do consume the turn with the wait-first loop (section 5) — `wait_agent` is
   the monitoring mechanism, not cursor polling.
5. Do delete the agent when done.
6. To verify a config change, do smoke-test with a throwaway agent:
   create → check → delete, before dispatching real work.

## 4. Child brief pattern

Do write briefs sent to sandbox agents so that they:

- Start with environment orientation: locate the repo/working copy, check
  credentials with `gh auth status`.
- STOP immediately and report the blocker if credentials are missing — never
  attempt workarounds, and never use any credential found for unrelated purposes.
- Practice secret hygiene: print environment variable NAMES only; for
  token-looking values, print the first 4 characters plus the length.
- End with a bounded structured summary of at most ~10 lines: OUTCOME (one
  line), URLS (one per line, issues/PRs created), NEXT (what's now eligible).

## 5. Consumption: the wait-first loop

`wait_agent` is the monitoring mechanism; cursor `read_events` is the tail-pull
mechanism, not the monitoring mechanism.

### Client tool-timeout caveat (read this first)

The host's MCP client tool-timeout must EXCEED the `wait_agent` timeout you
request — otherwise the client aborts the call early, before the server ever
wakes it, and the wait is wasted. Check or raise the client-side tool timeout
before requesting long waits; the server clamps requests to a 600s cap
(default 300s), but no server setting saves you from a client that times out
sooner.

### The canonical loop

1. `prompt_agent` — fire-and-forget; the call returns once the turn is
   accepted, not when the work finishes.
2. `get_agent` — orientation snapshot: current state plus latest assessment
   (verdict, summary, seq) before entering the wait.
3. `wait_agent(id, timeout)` — blocking. The host runs ZERO inference rounds
   while waiting ($0 while blocked). The wake response is a minimal snapshot:
   `changed` (kinds that fired: `state`, `assessment`, `timeout`), `state`,
   `assessment`. Repeated identical verdicts do NOT wake, and confidence-only
   changes do NOT wake — only a verdict transition, an FSM state change, a
   liveness failure, or timeout wakes you. On timeout: re-call `wait_agent`
   and keep waiting.
4. Route on the wake:
   - `looping` wake — the host decides: pull a bounded recent-transcript
     slice, judge, then steer or abort. Bound the judgment pull to the last
     ~20 records (~4K tokens) via cursor — enough to see the repeated
     pattern, never the full transcript.
   - `blocked` wake — fix the environment, then revive. The agent behaved
     correctly; never re-prompt unchanged.
   - `degraded` wake — halt new dispatches and diagnose the platform;
     fleet-wide, not agent-local.
   - idle (terminal) wake — read the terminal summary first. It preserves
     deliverable references (PR numbers, URLs, branch names, issue numbers)
     verbatim. Pull raw records AT MOST ONCE per turn, at the end, via ONE
     tail `read_events` pull — and only if the deliverables you need are not
     already in the terminal summary.

Do not loop `read_events` with an advancing cursor while the turn runs. That
is the cursor-polling pattern this loop replaces: per-poll inference over the
host's whole context for the entire turn.

### Fleet variant

For multiple children: `list_agents` returns each agent's state and latest
assessment — one call orients the whole fleet. Then issue parallel
`wait_agent` calls, one per child; each wait wakes independently. Consume the
same loop per child; on a `degraded` verdict, halt new dispatches fleet-wide
before diagnosing.

### Debugging boundary

Do not expect sandbox children in the host's observability stack — sandbox
agents do not export telemetry there, so the controller's event log IS the
debug channel for children. Host-side kape-controller tool calls (spans and
log rows) ARE visible to the host observability stack, if one is configured.

When a child misbehaves, do debug it by reading its event log via the
controller — do not look for child traces, spans, or logs in host-side
telemetry and conclude the child never ran.

## 6. Fallback path

If structured tool arguments are being dropped by the client (symptom: tool
calls arriving with empty params), do call the same server through the generic
MCP gateway tool — pass the tool name and args as a JSON object. It reaches the
same server.

Do not treat dropped structured arguments as a controller failure; it is a
client-side serialization problem, and the gateway path sidesteps it entirely.
