# runtime/tests/test_graph.py
import pytest
import json
from unittest.mock import AsyncMock, MagicMock
from datetime import datetime, timezone

from langchain_core.messages import SystemMessage, HumanMessage
from jinja2 import Environment

from kape_runtime.graph.state import AgentState
from kape_runtime.graph.nodes import make_entry_router, make_reason_node, make_respond_node
from kape_runtime.config import KapeConfig, LLMConfig
from kape_runtime.models import TaskStatus


def make_kape_config(**kwargs) -> KapeConfig:
    defaults = dict(
        handler_name="test-handler",
        handler_namespace="default",
        cluster_name="kind-local",
        dry_run=False,
        max_iterations=10,
        schema_name="test-schema",
        max_event_age_seconds=300,
    )
    defaults.update(kwargs)
    return KapeConfig(**defaults)


def make_llm_config(**kwargs) -> LLMConfig:
    defaults = dict(
        provider="anthropic",
        model="claude-haiku-4-5-20251001",
        system_prompt="You are an agent for {{ cluster_name }}.",
    )
    defaults.update(kwargs)
    return LLMConfig(**defaults)


def sample_cloud_event() -> dict:
    return {
        "specversion": "1.0",
        "type": "kape.events.alertmanager",
        "source": "alertmanager",
        "id": "evt-001",
        "time": "2026-04-18T10:00:00Z",
        "datacontenttype": "application/json",
        "data": {"alertname": "TestAlert"},
    }


def initial_state(event: dict | None = None) -> AgentState:
    return AgentState(
        event=event or sample_cloud_event(),
        task_id="01JXYZ",
        retry_task=None,
        messages=[],
        schema_output=None,
        parse_error=None,
        action_results=[],
        task_status=None,
        should_abort=False,
        dry_run=False,
    )


# --- entry_router ---

def test_entry_router_returns_reason_when_no_retry():
    router = make_entry_router()
    state = initial_state()
    result = router(state)
    assert result == "reason"


def test_entry_router_returns_reason_when_retry_of_present():
    router = make_entry_router()
    event = {**sample_cloud_event(), "retry_of": "01HOLD"}
    state = initial_state(event)
    result = router(state)
    assert result == "reason"


# --- reason node ---

@pytest.mark.asyncio
async def test_reason_node_renders_system_prompt_with_cluster_name():
    kape_cfg = make_kape_config()
    llm_cfg = make_llm_config(system_prompt="Agent for {{ cluster_name }}.")

    expected_output = {"decision": "ignore", "confidence": 0.9, "reasoning": "All good."}
    mock_structured_llm = MagicMock()
    mock_structured_llm.ainvoke = AsyncMock(return_value=expected_output)

    jinja_env = Environment()
    reason = make_reason_node(mock_structured_llm, kape_cfg, llm_cfg, jinja_env)

    state = initial_state()
    result = await reason(state)

    assert result["schema_output"] == expected_output
    assert result["parse_error"] is None

    messages = result["messages"]
    assert any(
        isinstance(m, SystemMessage) and "kind-local" in m.content
        for m in messages
    )


@pytest.mark.asyncio
async def test_reason_node_wraps_event_in_context_tags():
    kape_cfg = make_kape_config()
    llm_cfg = make_llm_config(system_prompt="Agent.")

    mock_structured_llm = MagicMock()
    mock_structured_llm.ainvoke = AsyncMock(return_value={"decision": "ignore", "confidence": 0.8, "reasoning": "OK"})
    jinja_env = Environment()
    reason = make_reason_node(mock_structured_llm, kape_cfg, llm_cfg, jinja_env)

    state = initial_state()
    result = await reason(state)

    messages = result["messages"]
    human_msgs = [m for m in messages if isinstance(m, HumanMessage)]
    assert len(human_msgs) == 1
    assert "<context>" in human_msgs[0].content
    assert "</context>" in human_msgs[0].content


@pytest.mark.asyncio
async def test_reason_node_captures_exception_as_parse_error():
    kape_cfg = make_kape_config()
    llm_cfg = make_llm_config()

    mock_structured_llm = MagicMock()
    mock_structured_llm.ainvoke = AsyncMock(side_effect=Exception("LLM failure"))
    jinja_env = Environment()
    reason = make_reason_node(mock_structured_llm, kape_cfg, llm_cfg, jinja_env)

    state = initial_state()
    result = await reason(state)

    assert result["schema_output"] is None
    assert "LLM failure" in result["parse_error"]


# --- respond node ---

def test_respond_node_sets_completed_when_output_present():
    respond = make_respond_node()
    state = initial_state()
    state["schema_output"] = {"decision": "ignore", "confidence": 0.9, "reasoning": "OK"}

    result = respond(state)
    assert result["task_status"] == TaskStatus.Completed
    assert result["should_abort"] is False


def test_respond_node_sets_schema_validation_failed_when_output_none():
    respond = make_respond_node()
    state = initial_state()
    state["schema_output"] = None
    state["parse_error"] = "ValidationError: field missing"

    result = respond(state)
    assert result["task_status"] == TaskStatus.SchemaValidationFailed
    assert result["should_abort"] is True
