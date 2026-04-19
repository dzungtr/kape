# Phase 7 — Full Runtime

**Status:** pending
**Milestone:** M3
**Specs:** 0004, 0006, 0013
**Modified by:** 0012 (created), 0013 (kapeproxy tool registry, load_skill tool, settings.toml [proxy] section)

## Goal

The runtime gains full agent capability: MCP tool calls via kapeproxy, vector memory, event-publish for handler chaining, retry/DLQ, dedup window, and Prometheus metrics.

## Reference Specs

- `0004-kape-handler` — MCP tool integration, ActionsRouter, memory tool, retry/DLQ, Prometheus metrics
- `0006-events-broker-design` — event-publish CloudEvent format, subject routing
- `0013-kape-skill-crd` — kapeproxy tool registry (single MCPToolkit), load_skill tool, settings.toml [proxy] section

## Work

### MCP tool integration (updated from 0013)

Replace per-tool sidecar connections with a single kapeproxy connection:

```python
# Single connection to kapeproxy federation endpoint
toolkit = MCPToolkit(url=config.proxy.endpoint)
mcp_tools = toolkit.get_tools()
# mcp_tools already contains namespaced tool names from kapeproxy
```

Add `call_tools` node (LangGraph `ToolNode`) to graph.
Full graph: `entry_router → reason ⇄ call_tools → respond`

### load_skill tool (new — from 0013)

Always registered in LangGraph tool registry at startup, regardless of lazy skills:

```python
from langchain_core.tools import tool
from pathlib import Path

SKILLS_DIR = Path("/etc/kape/skills")

@tool
def load_skill(skill_name: str) -> str:
    """
    Load the full instruction for a named skill.
    Call this when you determine a skill is relevant to the current investigation.
    Returns the full instruction text with all template variables resolved.
    """
    path = SKILLS_DIR / f"{skill_name}.txt"
    if not path.exists():
        return f"Skill '{skill_name}' not found. Available skills are listed in your instructions."
    raw = path.read_text()
    return jinja_env.from_string(raw).render(context)
```

`context` is the same Jinja2 render context used for the system prompt (`event`, `cluster_name`, `handler_name`, `namespace`, `timestamp`, `env`). If `SKILLS_DIR` does not exist, returns not-found message gracefully — no exception.

### settings.toml [proxy] section (from 0013)

The `[tools.*]` sections per MCP tool are replaced by a single `[proxy]` section:

```toml
[proxy]
endpoint  = "http://localhost:8080"
transport = "sse"
```

Memory-type tools retain their own section:
```toml
[tools.order-memory]
type            = "memory"
qdrant_endpoint = "http://kape-memory-order-memory.kape-system:6333"
```

Update `config.py` to read `[proxy]` instead of `[tools.*.sidecar_port]`.

### Memory tool integration
- Connect to Qdrant via `QDRANT_URL` + `QDRANT_COLLECTION` env vars
- Build LangChain `QdrantVectorStore` retriever
- Register as LangChain tool in LangGraph tool registry

### ActionsRouter (`actions/router.py`)
- JSONPath condition evaluation against `schema_output`
- `event-publish` action: resolve Jinja2 fields, extract `$prompt` fields, publish CloudEvent to NATS
- `webhook` action: HTTP POST to configured endpoint
- `save_memory` action: Qdrant upsert
- All actions run; partial failures logged, do not fail the Task

### Retry/DLQ
- Wrap LangGraph invoke with `tenacity` exponential backoff (LLM 429, 503)
- Non-retryable errors → immediate DLQ
- DLQ: publish to `kape.events.dlq.<handler-name>` with original CloudEvent + error context

### Dedup sliding window
- In-memory set of CloudEvent IDs, 60s TTL
- Reject duplicates before NATS ACK

### Prometheus metrics
- `prometheus_client` — `kape_events_total`, `kape_llm_latency_seconds`, `kape_tool_calls_total`, `kape_decisions_total`
- All labelled by handler name

## Acceptance Criteria

- Handler calls an MCP tool via kapeproxy during ReAct loop; namespaced tool name visible in OTEL trace
- Agent calls `load_skill("check-order-events")` → returns rendered instruction from `/etc/kape/skills/check-order-events.txt`
- Handler persists a memory entry to Qdrant; subsequent event retrieves it
- Handler A emits a CloudEvent via event-publish → Handler B picks it up; both Task records exist with correct `parent_task_id`
- Duplicate CloudEvent ID within 60s window is discarded
- LLM 429 triggers retry; non-retryable error routes to DLQ subject
- `curl handler-pod:8000/metrics` returns Prometheus text with all expected metrics

**M3 gate:** Two-handler chain runs end-to-end; MCP tool calls via kapeproxy appear in traces; load_skill works for lazy skills.

## Key Files

- `runtime/graph/graph.py` (updated: kapeproxy MCPToolkit, load_skill registered)
- `runtime/graph/nodes.py` (updated: call_tools node)
- `runtime/config.py` (updated: [proxy] section, remove per-tool sidecar config)
- `runtime/skills.py` (new: load_skill tool implementation)
- `runtime/actions/router.py`
- `runtime/actions/event_emitter.py`
- `runtime/actions/save_memory.py`
- `runtime/actions/webhook.py`
