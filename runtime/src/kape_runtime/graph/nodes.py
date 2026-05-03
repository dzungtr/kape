# runtime/src/kape_runtime/graph/nodes.py
from __future__ import annotations

import json
import logging
from datetime import datetime, timezone
from typing import Any, Callable, Sequence

from jinja2 import Environment
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import AIMessage, HumanMessage, SystemMessage
from langchain_core.runnables import Runnable
from langchain_core.tools import BaseTool

from kape_runtime.config import KapeConfig, LLMConfig
from kape_runtime.graph.state import AgentState
from kape_runtime.models import TaskStatus

logger = logging.getLogger(__name__)


def make_entry_router() -> Callable[[AgentState], str]:
    """Phase 4: always routes to 'reason'. Phase 7 adds ActionError retry path."""
    def entry_router(state: AgentState) -> str:
        return "reason"
    return entry_router


def make_reason_node(
    base_llm: BaseChatModel,
    mcp_tools: Sequence[BaseTool],
    kape_cfg: KapeConfig,
    llm_cfg: LLMConfig,
    jinja_env: Environment,
) -> Callable[[AgentState], dict]:
    """Bind MCP tools to the LLM and run a single ReAct turn.

    First turn (state.messages empty) seeds the conversation with the rendered
    system prompt + the event wrapped in <context> tags. Subsequent turns just
    append the LLM's AIMessage.
    """
    llm_with_tools = base_llm.bind_tools(list(mcp_tools))

    async def reason(state: AgentState) -> dict[str, Any]:
        if state["messages"]:
            sent = list(state["messages"])
            new_messages: list[Any] = []
        else:
            ctx = {
                "handler_name": kape_cfg.handler_name,
                "cluster_name": kape_cfg.cluster_name,
                "namespace": kape_cfg.handler_namespace,
                "timestamp": datetime.now(tz=timezone.utc).isoformat(),
                "event": state["event"],
            }
            rendered_prompt = jinja_env.from_string(llm_cfg.system_prompt).render(ctx)
            event_json = json.dumps(state["event"], default=str)
            seed = [
                SystemMessage(content=rendered_prompt),
                HumanMessage(content=f"<context>{event_json}</context>"),
            ]
            sent = seed
            new_messages = list(seed)

        ai_msg = await llm_with_tools.ainvoke(sent)
        new_messages.append(ai_msg)
        return {"messages": new_messages}

    return reason


def should_call_tools(state: AgentState) -> str:
    """Conditional edge: if the last AIMessage requested tools, run them; else respond."""
    if not state["messages"]:
        return "respond"
    last = state["messages"][-1]
    if isinstance(last, AIMessage) and getattr(last, "tool_calls", None):
        return "call_tools"
    return "respond"


def make_respond_node(structured_llm: Runnable) -> Callable[[AgentState], dict]:
    """Final pass: ask the schema-bound LLM to summarise the conversation into
    the required JSON schema, then set task_status."""
    async def respond(state: AgentState) -> dict[str, Any]:
        try:
            result = await structured_llm.ainvoke(list(state["messages"]))
            return {
                "schema_output": result,
                "parse_error": None,
                "task_status": TaskStatus.Completed,
                "should_abort": False,
            }
        except Exception as exc:
            logger.warning("Structured-output respond failed: %s", exc)
            return {
                "schema_output": None,
                "parse_error": str(exc),
                "task_status": TaskStatus.SchemaValidationFailed,
                "should_abort": True,
            }

    return respond
