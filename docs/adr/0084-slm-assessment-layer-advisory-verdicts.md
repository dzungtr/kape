# SLM assessment layer: advisory verdicts, controller-owned `agent.assessed` events

## Status

accepted

## Context

The host agents dispatching sandbox-agent work had to consume raw output (verbatim records, tool calls, stdout blobs) to know what their children were doing — monitoring tokens scaled with transcript size, per poll, all turn long, and each poll was an inference roundtrip over the host's whole context (spec #230). The session layer locks the durable event log as the single execution truth (ADR-0082) and keeps lifecycle events controller-owned (ADR-0083), so an SLM judge had to fit without becoming a second truth source.

## Decision

The controller runs an in-process SLM monitor that periodically assesses each running turn and writes **advisory** verdicts — `healthy | stuck | finished | failed` plus a short summary — as controller-owned `agent.assessed` events in the durable log; the latest assessment projects onto agent status as a minimal `{verdict, summary, assessedAt, seq}` property. The pipeline is cheap and fast by construction: purpose-built endpoints (`scaledown/summarize`, decision-modality `scaledown/classify-1`), temperature 0, no reasoning, capped output, 5s per-call timeout, 60s default tick (config knobs for interval and both model IDs), full turn log as the transcript window. The classifier sees summary plus turn telemetry (`secondsSinceLastRecord` hands the stuck fingerprint to the model as data, not inference). A terminal assessment runs synchronously in the settle path but hard-bounded by a 10s budget — settle proceeds without a verdict on failure, and mid-turn SLM failures are silent skips. The controller never acts on verdicts (observe-only; no auto-abort/auto-steer) and the FSM gains no `stuck` state: deterministic events stay the single execution truth, decision authority stays with the host agent. The SLM client targets a **configurable base URL** (default OpenRouter) — any OpenRouter-compatible endpoint (local gateway, proxy, alternative provider) can serve the pipeline; NetworkPolicy egress derives from the configured host. Full-log re-summarization has a known ~quadratic per-turn cost growth (~$0.36 at 1h dense) — accepted for v1, revisit trigger is the rolling-window + running-digest fallback.

## Consequences

Per-turn host-side monitoring cost drops from O(transcript × polls) of host-model tokens to pennies of OpenRouter input; the assessment history is durable and auditable via `read_events` like every other event; a missing API key disables the monitor with everything else unaffected. Verdict quality is only as good as the SLM — mitigated by the seq anchor (hosts can detect stale verdicts) and by observe-only semantics. A new event type enters the durable log vocabulary, so its schema is a cross-slice handoff (spec #230). Spec: #230.
