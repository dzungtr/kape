# Use a stub MCP server with canned responses for playground tooling

## Status

accepted

## Context

Testing the runtime's ReAct loop end-to-end requires MCP tools, but wiring real MCP servers (pod logs, metrics, nodes) into a local playground is heavy and non-deterministic.

## Decision

Provide a minimal Python FastAPI stub MCP server (`playground/stub-mcp/`) exposing `get_pod_logs`, `list_nodes`, and `query_metrics` with fixed canned responses over SSE transport on port 8090, with an `MCP_CONFIG_PATH` escape hatch to swap in real MCP servers.

## Consequences

The runtime can be exercised deterministically without real cluster tooling, matching the runtime's `proxy.transport = sse` config. Advanced cases requiring real tools must override `MCP_CONFIG_PATH` and update `proxy.endpoint` in `settings.toml`.

## Source

- [2026-05-03-local-dev-playground-design.md](../../superpowers/specs/2026-05-03-local-dev-playground-design.md)
