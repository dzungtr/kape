# Keep NetworkPolicy generation manual rather than operator-automated

## Status

accepted

## Context

The operator could in principle generate NetworkPolicies automatically from KapeTool definitions, but doing so expands operator scope and couples network policy to the operator's lifecycle rather than the engineer's deployment workflow.

## Decision

Ship the manifests as reference examples that engineers apply manually during cluster setup; auto-generation is deferred to v2 (spec 0007 Known Gaps).

## Consequences

Operator scope stays tight and engineers control network policy as part of their GitOps manifests. The tradeoff is manual upkeep: adding a KapeTool or component requires hand-authoring and applying the matching policy, with no automated drift detection.

## Source

- [2026-05-17-phase8-issue-77-network-policy-design.md](../../superpowers/specs/2026-05-17-phase8-issue-77-network-policy-design.md)
