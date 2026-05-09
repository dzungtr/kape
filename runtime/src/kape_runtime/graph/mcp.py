# runtime/src/kape_runtime/graph/mcp.py
from __future__ import annotations

from langchain_core.tools import BaseTool
from langchain_mcp_adapters.client import MultiServerMCPClient

from kape_runtime.config import ProxyConfig


async def build_mcp_tools(proxy_cfg: ProxyConfig) -> list[BaseTool]:
    """Connect to the kapeproxy federation endpoint and return its MCP tools.

    A single MultiServerMCPClient federation handles all sidecar tools — the
    runtime no longer maintains per-tool MCP connections.
    """
    client = MultiServerMCPClient(
        {
            "kapeproxy": {
                "url": proxy_cfg.endpoint,
                "transport": proxy_cfg.transport,
            }
        }
    )
    return await client.get_tools()
