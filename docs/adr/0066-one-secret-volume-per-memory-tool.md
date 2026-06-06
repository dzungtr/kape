# Use one secret volume per memory tool instead of a shared volume

## Status

accepted

## Context

Multiple memory-type KapeTools could attach to a handler, and a single shared secrets volume would force cross-tool key namespacing and couple unrelated secret lifecycles together.

## Decision

Provision one Secret-backed Volume and VolumeMount per memory tool, with the tool name embedded in the mount path (`/etc/kape/secrets/<tool-name>`), naming the Secret `kape-tool-<name>-conn` and the volume `kape-tool-<name>-secrets`.

## Consequences

Avoids cross-tool secret collisions and gives each Secret its own lifecycle, but the runtime needs `KAPE_TOOL_NAME` injected to construct the path. For v1 only one memory tool per handler is supported; if more are detected the operator emits a warning condition to `KapeHandler.status` and uses the first tool's name.

## Source

- [2026-05-17-phase8-issue-79-secret-management-design.md](../../superpowers/specs/2026-05-17-phase8-issue-79-secret-management-design.md)
