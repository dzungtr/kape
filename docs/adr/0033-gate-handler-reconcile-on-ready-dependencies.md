# Gate handler reconciliation on Ready dependencies before materializing resources

## Status

accepted

## Context

A KapeHandler depends on a KapeSchema and zero or more KapeTools that may not yet exist or be ready, raising the question of how the handler reconciler should behave when its inputs are incomplete. Proceeding to render config and create Deployments against unready dependencies would produce broken pods.

## Decision

The handler reconcile begins with a hard dependency gate that requires the referenced KapeSchema and every referenced KapeTool to exist and report `Ready=True`, surfacing `DependenciesReady=False` with a specific reason and requeueing until all dependencies pass.

## Consequences

Handler Deployments are only created once all inputs are valid and ready, preventing partially-configured pods. The handler's status clearly reflects which dependency is blocking, but reconciliation is held in a Pending state with periodic requeues until dependencies resolve.

## Source

- [2026-04-19-phase6-full-operator-design.md](../../superpowers/specs/2026-04-19-phase6-full-operator-design.md)
