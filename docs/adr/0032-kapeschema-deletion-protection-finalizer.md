# Block KapeSchema deletion while referenced via a deletion-protection finalizer

## Status

accepted

## Context

KapeSchemas are referenced by KapeHandlers, so deleting a schema out from under live handlers would break them. The operator needs a safety mechanism to prevent orphaning handlers that depend on a schema.

## Decision

`KapeSchemaReconciler` manages a `kape.io/schema-protection` finalizer: on deletion it lists KapeHandlers labelled `kape.io/schema-ref={name}` and blocks removal (emitting a Warning event) until no references remain.

## Consequences

Schemas in active use cannot be accidentally deleted, giving operators a clear error instead of silent handler breakage. This relies on the `kape.io/schema-ref` label being kept in sync on handlers and adds finalizer lifecycle handling to every schema reconcile.

## Source

- [2026-04-19-phase6-full-operator-design.md](../../superpowers/specs/2026-04-19-phase6-full-operator-design.md)
