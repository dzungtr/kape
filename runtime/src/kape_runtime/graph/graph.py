# runtime/src/kape_runtime/graph/graph.py
from __future__ import annotations

from jinja2 import Environment, select_autoescape
from langchain_core.language_models import BaseChatModel
from langgraph.graph import END, START, StateGraph

from kape_runtime.config import KapeConfig, LLMConfig, SchemaConfig
from kape_runtime.graph.nodes import (
    make_entry_router,
    make_reason_node,
    make_respond_node,
)
from kape_runtime.graph.state import AgentState
from kape_runtime.schema_loader import make_structured_llm


def build_graph(
    llm: BaseChatModel,
    kape_cfg: KapeConfig,
    llm_cfg: LLMConfig,
    schema_cfg: SchemaConfig,
) -> object:
    """Build and compile the Phase 4 minimal LangGraph: entry_router → reason → respond."""
    # select_autoescape([]) explicitly opts out of HTML escaping for LLM prompt templates
    jinja_env = Environment(autoescape=select_autoescape([]))
    structured_llm = make_structured_llm(llm, schema_cfg.json_schema)

    graph = StateGraph(AgentState)

    graph.add_node("entry_router_node", lambda state: {})
    graph.add_node("reason", make_reason_node(structured_llm, kape_cfg, llm_cfg, jinja_env))
    graph.add_node("respond", make_respond_node())

    graph.add_edge(START, "entry_router_node")
    graph.add_conditional_edges(
        "entry_router_node",
        make_entry_router(),
        {"reason": "reason"},
    )
    graph.add_edge("reason", "respond")
    graph.add_edge("respond", END)

    return graph.compile()
