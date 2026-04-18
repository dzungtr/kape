# runtime/src/kape_runtime/graph/state.py
from __future__ import annotations

from typing import Any

from langchain_core.messages import BaseMessage
from typing_extensions import TypedDict

from kape_runtime.models import ActionResult, TaskStatus


class AgentState(TypedDict):
    # Input
    event: dict[str, Any]
    task_id: str
    retry_task: dict | None

    # Reasoning
    messages: list[BaseMessage]

    # Output
    schema_output: dict[str, Any] | None
    parse_error: str | None

    # Actions (Phase 7)
    action_results: list[ActionResult]
    task_status: TaskStatus | None

    # Control
    should_abort: bool
    dry_run: bool
