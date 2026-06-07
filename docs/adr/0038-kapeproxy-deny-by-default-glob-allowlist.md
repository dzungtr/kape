# Use deny-by-default glob intersection for KapeProxy allowedTools

## Status

accepted

## Context

KapeProxy's `allowedTools` field was implemented as a literal list of exact names emitted verbatim, which let `tools/list` advertise tools the upstream cannot serve and let `tools/call` accept calls to nonexistent tools. The original D14 semantics also treated an empty/omitted `allowedTools` as "expose all", creating an implicit security hole where adding a new upstream silently exposed every tool it advertised and leaked the upstream catalog to clients.

## Decision

`allowedTools` entries are shell-style glob patterns evaluated with stdlib `path.Match`, and the exposed tool set is the intersection of the upstream's real tool list with the union of glob matches; both `tools/list` and `tools/call` compute the same set, with nil/omitted/empty meaning deny-all and `["*"]` meaning allow-all.

## Consequences

Operators must explicitly opt in to forwarding (e.g. `allowedTools: ["k8s_*"]` or `["*"]`), making this a breaking change for any KapeTool with omitted/empty `allowedTools`, which now contributes zero tools. `tools/list` and `tools/call` stay consistent, stale allowlist entries silently drop out instead of routing dead calls, and rejected calls return MCP error `-32601` so clients cannot distinguish "not allowed" from "does not exist". Malformed globs are logged at startup and treated as matching nothing rather than crashing the proxy.

## Source

- [2026-05-17-kapeproxy-slice7-fixup.md](../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md)
