# Phase 7.1 — Proxy Config

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0004, 0013

## Goal

Update `config.py` to read a single `[proxy]` section from `settings.toml`, replacing the per-tool `[tools.*.sidecar_port]` configuration. Memory-type tools retain their own `[tools.*]` section.

## Work

- Update `Config` dataclass: replace `tools: dict[str, ToolConfig]` sidecar fields with `proxy: ProxyConfig` (fields: `endpoint: str`, `transport: str`)
- Add `ProxyConfig` dataclass
- Memory-type tool config remains under `[tools.<name>]` with `type = "memory"` — only sidecar/MCP tool config is removed
- Update example `settings.toml` to reflect new schema:
  ```toml
  [proxy]
  endpoint  = "http://localhost:8080"
  transport = "sse"

  [tools.order-memory]
  type            = "memory"
  qdrant_endpoint = "http://kape-memory-order-memory.kape-system:6333"
  ```
- Update unit tests for config loading

## Acceptance Criteria

- `Config` loads `proxy.endpoint` and `proxy.transport` from `[proxy]` section
- Memory-type tool config still loads correctly from `[tools.*]`
- Old `sidecar_port` config key is removed; loading a settings.toml with it raises a clear validation error

## Key Files

- `runtime/config.py` (modified)
- `runtime/tests/test_config.py` (modified)
