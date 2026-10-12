# Controller↔daemon contract: pull transport, passive daemon, controller seq authority

## Status

accepted

## Context

M2 direction (#150): the daemon-in-sandbox owns the live pi RPC connection (#153); the controller is dropable and calls the daemon by API (#154). Epic 2 (#191) resolved the contract (#164) and spawning (#167) on top of Epic 1's log-projected session layer (ADR-0082, spec #194). The natural shapes — daemon pushing records to the controller, the daemon owning the log, or a kape-owned gRPC service — all invert something Epic 1 had already locked or open the control plane to sandbox egress.

## Decision

The controller pulls; the daemon never dials out. The daemon is a passive HTTP server (REST + long-poll): the controller long-polls `GET /records?after=<ack cursor>` (the poll is the heartbeat), commands ride plain `POST`s, and the ack for drained records is the next pull's cursor. Sandbox→controller egress is denied by network policy; the daemon carries no credentials for the controller. The controller stays **seq authority**: the daemon spools unacked records on the sandbox PVC (monotonic spool ids durable across daemon restarts) and the controller appends them into the durable event log, deduping re-pushes via a store-side origin watermark.

Daemon death means pi death (the daemon owns the pipes): the in-flight turn is lost (#159 crash window). A reconnect preflight distinguishes partition (same `boot_id`) — seamless spool resume, no durable events — from daemon death (new `boot_id`): drain the spool as honest history, append a `daemon.boot` event, mark agent liveness failed while the session FSM stays `running` (sessions have no failed state; the turn never settled). Address: `Sandbox` CR `status.podIPs` primary, headless-Service DNS fallback — the controller's first k8s API client. Sandbox subagents are created through the front door with parent linkage and pre-gateway caps; in-sandbox parallelism is the agent's own business. Auth direction is locked (controller→daemon credential only); the scheme stays open pending injection research.

## Consequences

Two cursors exist by design: the client-facing per-agent seq (controller-assigned, Epic 1) and the daemon spool ack cursor (the pull's `after`) — the origin watermark joins them, so a controller crash re-pulls without duplicates. Controller downtime pauses projection and client serving but never kills a pi turn; sandbox deletion no longer destroys history. Lifecycle/session events stay controller-owned; the daemon emits only verbatim pi records and notices. The front door grows steer (9 tools); `clear_queue` stays ops break-glass. Epic 3 must add the `running → paused` edge the transition table lacks. Spec: #209.
