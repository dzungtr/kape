# runtime/src/kape_runtime/graph/graph.py
from __future__ import annotations

from typing import Sequence

from jinja2 import Environment, select_autoescape
from langchain_core.language_models import BaseChatModel
from langchain_core.tools import BaseTool
from langgraph.graph import END, START, StateGraph
from langgraph.prebuilt import ToolNode

from kape_runtime.config import KapeConfig, LLMConfig, SchemaConfig
from kape_runtime.graph.nodes import (
    make_entry_router,
    make_reason_node,
    make_respond_node,
    should_call_tools,
)
from kape_runtime.graph.state import AgentState
from kape_runtime.memory import build_memory_tool
from kape_runtime.schema_loader import make_structured_llm
from kape_runtime.skills import make_load_skill_tool


def build_graph(
    llm: BaseChatModel,
    kape_cfg: KapeConfig,
    llm_cfg: LLMConfig,
    schema_cfg: SchemaConfig,
    mcp_tools: Sequence[BaseTool],
) -> object:
    """Build and compile the Phase 7.2 ReAct graph:

        START -> entry_router -> reason -> [call_tools -> reason]* -> respond -> END

    The reason node binds `mcp_tools` to the LLM each turn. When the LLM emits
    tool_calls the graph routes through `call_tools` (a LangGraph ToolNode) and
    loops back into reason. When the LLM stops emitting tool_calls, the
    structured-output `respond` node summarises the conversation into the
    schema-validated decision.
    """
    jinja_env = Environment(autoescape=select_autoescape([]))
    structured_llm = make_structured_llm(llm, schema_cfg.json_schema)

    tools: list[BaseTool] = list(mcp_tools)
    if (memory_tool := build_memory_tool()) is not None:
        tools.append(memory_tool)
    render_ctx = {
        "handler_name": kape_cfg.handler_name,
        "cluster_name": kape_cfg.cluster_name,
        "namespace": kape_cfg.handler_namespace,
    }
    tools.append(make_load_skill_tool(jinja_env, render_ctx))

    graph = StateGraph(AgentState)

    graph.add_node("entry_router_node", lambda state: {})
    graph.add_node("reason", make_reason_node(llm, tools, kape_cfg, llm_cfg, jinja_env))
    graph.add_node("call_tools", ToolNode(tools))
    graph.add_node("respond", make_respond_node(structured_llm))

    graph.add_edge(START, "entry_router_node")
    graph.add_conditional_edges(
        "entry_router_node",
        make_entry_router(),
        {"reason": "reason"},
    )
    graph.add_conditional_edges(
        "reason",
        should_call_tools,
        {"call_tools": "call_tools", "respond": "respond"},
    )
    graph.add_edge("call_tools", "reason")
    graph.add_edge("respond", END)

    return graph.compile()
