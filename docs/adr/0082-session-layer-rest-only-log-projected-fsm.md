# Session layer: REST-only identity, FSM projected from the durable event log

## Status

accepted

## Context

The greenfield successor (map #150) locked a dropable controller (#154) and a daemon-owned data plane (#153). Epic 1 (#190) resolved how sessions are identified and who owns their state (#160, #155). The old platform was CRD-first, so a KapeSession CRD looked like the natural front door; the FSM alternatives were KapeSession reconcile or Temporal durable execution.

## Decision

No KapeSession CRD in M2: the controller's REST/MCP tool contract is the session front door; upstream agent-sandbox `Sandbox` CRs are an address source, never the front door. Session state is a controller-internal FSM projected from the durable event log — the log is the single execution truth; CRs and controller views are intent mirrors. No Temporal.

## Consequences

Session traffic stays imperative (prompt/abort are verbs, not desired state) with no CR status mirror to drift. The controller restarts stateless-in-effect: replay the log, state returns. Declarative/GitOps entry, if ever, targets upstream CRs (#168, M3). Revive budget and `pi --resume` orchestration drive from the log (#159, #192). Spec: #194.
