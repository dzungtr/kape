# runtime/src/kape_runtime/graph/state.py
from __future__ import annotations

from typing import Annotated, Any

from langchain_core.messages import BaseMessage
from langgraph.graph.message import add_messages
from typing_extensions import TypedDict

from kape_runtime.models import ActionResult, TaskStatus


class AgentState(TypedDict):
    # Input
    event: dict[str, Any]
    task_id: str
    retry_task: dict | None

    # Reasoning
    messages: Annotated[list[BaseMessage], add_messages]

    # Output
    schema_output: dict[str, Any] | None
    parse_error: str | None

    # Actions (Phase 7)
    action_results: list[ActionResult]
    task_status: TaskStatus | None

    # Control
    should_abort: bool
    dry_run: bool
