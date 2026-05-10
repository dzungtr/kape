# kapeproxy

This module contains the kapeproxy MCP proxy binary for kape-io handlers.

## Stub image (Phase 6 Slice 5)

The `kape/kapeproxy:stub` image is a **transitional, non-production** binary.
It reads `/etc/kapeproxy/config.yaml` (rendered by the operator) and serves a
static `tools/list` response with namespaced tool names (`{kapetool}__{toolname}`).
All `tools/call` requests return MCP error `-32603 server not yet available`.

The stub image is built and pushed by the slice-5 CI workflow. It is removed in
**Phase 6 Slice 7**, which ships the real kapeproxy binary.

Do NOT use the stub image as a stable artifact. It carries no backwards-compatibility
guarantee and will be deleted from the registry after Slice 7 merges.

## Real binary (Phase 6 Slice 7)

Replaces the stub. Implements full MCP proxying with allowlist filtering, JSONPath
redaction, audit logging, and OTEL tracing.
