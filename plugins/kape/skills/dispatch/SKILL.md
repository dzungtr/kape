---
name: dispatch
description: USE FOR dispatching work to kape-controller sandbox agents — creating, prompting, polling, or deleting agents via the kape-controller MCP server; debugging sandbox-agent children; or any task mentioning kape-controller. Covers dispatch lifecycle, error classification, and output consumption.
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
4. Do poll events by cursor: first poll after a warm-up delay, then advance the
   cursor (`after=last-seen`) and escalate the limit if needed. Never re-pull
   from zero.
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

## 5. Output consumption and debugging boundary

Do consume sandbox-agent output through the controller's event log
(cursor-pull). Do not expect sandbox children in the host's observability stack —
sandbox agents do not export telemetry there, so the event log IS the debug
channel for children. Host-side kape-controller tool calls (spans and log rows)
ARE visible to the host observability stack, if one is configured.

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
