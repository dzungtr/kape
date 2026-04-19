# Phase 7.4 — Memory Tool

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0004

## Goal

Connect to Qdrant via environment variables and register a `QdrantVectorStore` retriever as a LangChain tool in the graph tool registry.

## Work

- Create `runtime/memory.py`:
  - Read `QDRANT_URL` and `QDRANT_COLLECTION` from env
  - Build `QdrantVectorStore` client
  - Wrap as a LangChain `Tool` with name `search_memory` and description for the LLM
  - Return `None` if env vars not set (handler may not have a memory tool configured)
- In `graph.py`: if `memory_tool := build_memory_tool()` is not None, append to tool registry
- Write integration test using a local Qdrant instance (or mock)

## Acceptance Criteria

- Handler persists a memory entry to Qdrant via `save_memory` action (Phase 7.5)
- Subsequent event retrieves the stored entry via `search_memory` tool call
- No memory tool configured (`QDRANT_URL` unset) → graph starts normally, `search_memory` absent from tool list

## Key Files

- `runtime/memory.py` (new)
- `runtime/graph/graph.py` (modified — register memory tool)
- `runtime/tests/test_memory.py` (new)
