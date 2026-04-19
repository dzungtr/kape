# Phase 7.2 — Kapeproxy MCP Integration

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0004, 0013

## Goal

Replace per-tool sidecar MCPToolkit connections with a single `MCPToolkit` connecting to the kapeproxy federation endpoint. Wire a `call_tools` LangGraph `ToolNode` into the graph, completing the full `entry_router → reason ⇄ call_tools → respond` loop.

## Work

- Replace multiple per-tool `MCPToolkit` instantiations with one:
  ```python
  toolkit = MCPToolkit(url=config.proxy.endpoint)
  mcp_tools = toolkit.get_tools()
  ```
- Add `call_tools` node using LangGraph `ToolNode(mcp_tools)`
- Wire full graph: `entry_router → reason → call_tools → reason` (loop) `→ respond`
- Add conditional edge from `reason`: if `tool_calls` present → `call_tools`, else → `respond`
- Remove all per-tool sidecar connection code

## Acceptance Criteria

- Handler calls an MCP tool via kapeproxy during ReAct loop
- Namespaced tool name (e.g. `kapetool-name__tool-name`) visible in OTEL trace
- Graph terminates at `respond` when LLM produces no tool calls

## Key Files

- `runtime/graph/graph.py` (modified)
- `runtime/graph/nodes.py` (modified — add call_tools node)
- `runtime/tests/test_graph.py` (modified)
