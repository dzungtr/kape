# runtime/src/kape_runtime/graph/nodes.py
from __future__ import annotations

import json
import logging
from datetime import datetime, timezone
from typing import Any, Callable

from jinja2 import Environment
from langchain_core.messages import HumanMessage, SystemMessage
from langchain_core.runnables import Runnable

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
    structured_llm: Runnable,
    kape_cfg: KapeConfig,
    llm_cfg: LLMConfig,
    jinja_env: Environment,
) -> Callable[[AgentState], dict]:
    """Renders Jinja2 system prompt, calls structured LLM, captures output or exception."""
    async def reason(state: AgentState) -> dict[str, Any]:
        ctx = {
            "handler_name": kape_cfg.handler_name,
            "cluster_name": kape_cfg.cluster_name,
            "namespace": kape_cfg.handler_namespace,
            "timestamp": datetime.now(tz=timezone.utc).isoformat(),
            "event": state["event"],
        }
        rendered_prompt = jinja_env.from_string(llm_cfg.system_prompt).render(ctx)
        event_json = json.dumps(state["event"], default=str)

        messages = [
            SystemMessage(content=rendered_prompt),
            HumanMessage(content=f"<context>{event_json}</context>"),
        ]

        try:
            result = await structured_llm(messages)
            return {"schema_output": result, "parse_error": None, "messages": messages}
        except Exception as exc:
            logger.warning("LLM structured output failed: %s", exc)
            return {
                "schema_output": None,
                "parse_error": str(exc),
                "messages": messages,
            }

    return reason


def make_respond_node() -> Callable[[AgentState], dict]:
    """Sets task_status based on whether schema_output is present. No actions in Phase 4."""
    def respond(state: AgentState) -> dict[str, Any]:
        if state["schema_output"] is None:
            return {
                "task_status": TaskStatus.SchemaValidationFailed,
                "should_abort": True,
            }
        return {
            "task_status": TaskStatus.Completed,
            "should_abort": False,
        }
    return respond
