# Trigger handler rollouts via a content hash over handler, schema, and tool specs

## Status

accepted

## Context

When a referenced KapeSchema or KapeTool changes, the dependent handler Deployments must roll out to pick up new configuration, raising the question of how to detect and signal config drift. The operator needs a deterministic way to know when a redeploy is required.

## Decision

Compute `rolloutHash = sha256(KapeHandler.spec + KapeSchema.spec + all referenced KapeTool.spec)` and write it as the pod annotation `kape.io/rollout-hash`; cross-resource watches re-enqueue handlers when a dependency's hash changes.

## Consequences

Any change to a handler's own spec or any of its dependencies deterministically alters the pod annotation and triggers a Deployment rollout. This requires the reconciler to read schema and tool specs on every handler reconcile and depends on the secondary watches plus label field indexes for efficient fan-out.

## Source

- [2026-04-19-phase6-full-operator-design.md](../../superpowers/specs/2026-04-19-phase6-full-operator-design.md)
