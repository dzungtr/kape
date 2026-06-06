# Use file-first read with env var fallback for memory credentials

## Status

accepted

## Context

Production handlers receive secrets via mounted files, but developers running the runtime locally have no secret infrastructure and need a working path without mounts.

## Decision

`build_memory_tool()` reads `qdrant_url`/`qdrant_collection` from the mounted file path when `KAPE_TOOL_NAME` and the file exist, otherwise falls back to `QDRANT_URL`/`QDRANT_COLLECTION` env vars, returning None if neither resolves both values.

## Consequences

Preserves backward compatibility for local development while production always uses files, and file values take precedence over env vars. Handlers without a resolvable memory backend simply omit the `search_memory` tool from the graph.

## Source

- [2026-05-17-phase8-issue-79-secret-management-design.md](../../superpowers/specs/2026-05-17-phase8-issue-79-secret-management-design.md)
