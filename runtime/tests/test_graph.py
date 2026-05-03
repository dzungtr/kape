# runtime/tests/test_graph.py
import pytest
from unittest.mock import AsyncMock, MagicMock

from langchain_core.messages import (
    AIMessage,
    HumanMessage,
    SystemMessage,
    ToolMessage,
)
from langchain_core.tools import tool
from jinja2 import Environment

from kape_runtime.graph.state import AgentState
from kape_runtime.graph.nodes import (
    make_entry_router,
    make_reason_node,
    make_respond_node,
    should_call_tools,
)
from kape_runtime.config import KapeConfig, LLMConfig, SchemaConfig
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


def initial_state(event: dict | None = None, messages: list | None = None) -> AgentState:
    return AgentState(
        event=event or sample_cloud_event(),
        task_id="01JXYZ",
        retry_task=None,
        messages=messages or [],
        schema_output=None,
        parse_error=None,
        action_results=[],
        task_status=None,
        should_abort=False,
        dry_run=False,
    )


@tool
def fake_mcp_tool(query: str) -> str:
    """A fake MCP tool for testing."""
    return f"result for {query}"


# --- entry_router ---

def test_entry_router_returns_reason_when_no_retry():
    router = make_entry_router()
    state = initial_state()
    assert router(state) == "reason"


def test_entry_router_returns_reason_when_retry_of_present():
    router = make_entry_router()
    event = {**sample_cloud_event(), "retry_of": "01HOLD"}
    state = initial_state(event)
    assert router(state) == "reason"


# --- reason node ---

@pytest.mark.asyncio
async def test_reason_node_seeds_messages_with_system_and_human_when_empty():
    kape_cfg = make_kape_config()
    llm_cfg = make_llm_config(system_prompt="Agent for {{ cluster_name }}.")

    bound_llm = MagicMock()
    bound_llm.ainvoke = AsyncMock(return_value=AIMessage(content="ok"))
    base_llm = MagicMock()
    base_llm.bind_tools = MagicMock(return_value=bound_llm)

    reason = make_reason_node(base_llm, [fake_mcp_tool], kape_cfg, llm_cfg, Environment())

    state = initial_state()
    result = await reason(state)

    sent_messages = bound_llm.ainvoke.call_args.args[0]
    assert isinstance(sent_messages[0], SystemMessage)
    assert "kind-local" in sent_messages[0].content
    assert isinstance(sent_messages[1], HumanMessage)
    assert "<context>" in sent_messages[1].content

    assert "messages" in result
    appended = result["messages"]
    assert isinstance(appended[0], SystemMessage)
    assert isinstance(appended[1], HumanMessage)
    assert isinstance(appended[2], AIMessage)


@pytest.mark.asyncio
async def test_reason_node_appends_only_ai_message_when_history_present():
    kape_cfg = make_kape_config()
    llm_cfg = make_llm_config()

    bound_llm = MagicMock()
    bound_llm.ainvoke = AsyncMock(return_value=AIMessage(content="second turn"))
    base_llm = MagicMock()
    base_llm.bind_tools = MagicMock(return_value=bound_llm)

    reason = make_reason_node(base_llm, [fake_mcp_tool], kape_cfg, llm_cfg, Environment())

    history = [
        SystemMessage(content="seed"),
        HumanMessage(content="<context>{}</context>"),
        AIMessage(content="first turn"),
        ToolMessage(content="tool result", tool_call_id="call_1"),
    ]
    state = initial_state(messages=history)
    result = await reason(state)

    assert result["messages"] == [AIMessage(content="second turn")]
    sent = bound_llm.ainvoke.call_args.args[0]
    assert sent == history


@pytest.mark.asyncio
async def test_reason_node_binds_mcp_tools_to_llm():
    kape_cfg = make_kape_config()
    llm_cfg = make_llm_config()

    bound_llm = MagicMock()
    bound_llm.ainvoke = AsyncMock(return_value=AIMessage(content="ok"))
    base_llm = MagicMock()
    base_llm.bind_tools = MagicMock(return_value=bound_llm)

    tools = [fake_mcp_tool]
    reason = make_reason_node(base_llm, tools, kape_cfg, llm_cfg, Environment())

    await reason(initial_state())

    base_llm.bind_tools.assert_called_once_with(tools)


# --- should_call_tools router ---

def test_should_call_tools_routes_to_call_tools_when_tool_calls_present():
    state = initial_state(messages=[
        AIMessage(
            content="",
            tool_calls=[{"name": "fake_mcp_tool", "args": {"query": "x"}, "id": "c1"}],
        ),
    ])
    assert should_call_tools(state) == "call_tools"


def test_should_call_tools_routes_to_respond_when_no_tool_calls():
    state = initial_state(messages=[AIMessage(content="final answer")])
    assert should_call_tools(state) == "respond"


# --- respond node ---

@pytest.mark.asyncio
async def test_respond_node_calls_structured_llm_with_messages_and_sets_completed():
    expected_output = {"decision": "ignore", "confidence": 0.9, "reasoning": "All good."}
    structured_llm = MagicMock()
    structured_llm.ainvoke = AsyncMock(return_value=expected_output)

    respond = make_respond_node(structured_llm)

    history = [
        SystemMessage(content="seed"),
        HumanMessage(content="<context>{}</context>"),
        AIMessage(content="final"),
    ]
    state = initial_state(messages=history)
    result = await respond(state)

    structured_llm.ainvoke.assert_awaited_once_with(history)
    assert result["schema_output"] == expected_output
    assert result["task_status"] == TaskStatus.Completed
    assert result["should_abort"] is False
    assert result["parse_error"] is None


@pytest.mark.asyncio
async def test_respond_node_sets_schema_validation_failed_when_structured_llm_raises():
    structured_llm = MagicMock()
    structured_llm.ainvoke = AsyncMock(side_effect=Exception("bad schema"))

    respond = make_respond_node(structured_llm)
    state = initial_state(messages=[AIMessage(content="x")])
    result = await respond(state)

    assert result["schema_output"] is None
    assert result["task_status"] == TaskStatus.SchemaValidationFailed
    assert result["should_abort"] is True
    assert "bad schema" in result["parse_error"]


# --- graph assembly ---

from kape_runtime.graph.graph import build_graph


def _build_base_llm(ai_responses: list[AIMessage]) -> MagicMock:
    """Build a mock base_llm whose bind_tools(...) returns an LLM whose ainvoke
    yields the given AIMessages in order across calls."""
    bound = MagicMock()
    it = iter(ai_responses)
    async def _ainvoke(_msgs):
        return next(it)
    bound.ainvoke = AsyncMock(side_effect=_ainvoke)
    base = MagicMock()
    base.bind_tools = MagicMock(return_value=bound)
    return base, bound


@pytest.mark.asyncio
async def test_build_graph_routes_to_respond_when_no_tool_calls():
    kape_cfg = make_kape_config()
    llm_cfg = make_llm_config()
    schema_cfg = SchemaConfig(
        name="test",
        json_schema={"type": "object", "properties": {"decision": {"type": "string"}}},
    )

    base_llm, _ = _build_base_llm([AIMessage(content="no tool needed")])

    expected_output = {"decision": "ignore", "confidence": 0.9, "reasoning": "OK"}
    structured_ainvoke = AsyncMock(return_value=expected_output)
    base_llm.with_structured_output = MagicMock(
        return_value=MagicMock(ainvoke=structured_ainvoke)
    )

    graph = build_graph(
        base_llm, kape_cfg, llm_cfg, schema_cfg, mcp_tools=[fake_mcp_tool]
    )
    result = await graph.ainvoke(initial_state())

    assert result["task_status"] == TaskStatus.Completed
    assert result["schema_output"] == expected_output


@pytest.mark.asyncio
async def test_build_graph_invokes_tool_when_llm_emits_tool_call_then_terminates():
    kape_cfg = make_kape_config()
    llm_cfg = make_llm_config()
    schema_cfg = SchemaConfig(
        name="test",
        json_schema={"type": "object", "properties": {"decision": {"type": "string"}}},
    )

    tool_call_msg = AIMessage(
        content="",
        tool_calls=[{"name": "fake_mcp_tool", "args": {"query": "hello"}, "id": "c1"}],
    )
    final_msg = AIMessage(content="done")
    base_llm, bound_llm = _build_base_llm([tool_call_msg, final_msg])

    expected_output = {"decision": "remediate", "confidence": 0.8, "reasoning": "ran tool"}
    structured_ainvoke = AsyncMock(return_value=expected_output)
    base_llm.with_structured_output = MagicMock(
        return_value=MagicMock(ainvoke=structured_ainvoke)
    )

    graph = build_graph(
        base_llm, kape_cfg, llm_cfg, schema_cfg, mcp_tools=[fake_mcp_tool]
    )
    result = await graph.ainvoke(initial_state())

    # bound_llm.ainvoke called twice: first turn (emits tool_call) + second turn (final)
    assert bound_llm.ainvoke.await_count == 2
    # respond node calls structured_llm exactly once at the end
    structured_ainvoke.assert_awaited_once()
    assert result["schema_output"] == expected_output
    assert result["task_status"] == TaskStatus.Completed

    # ToolMessage from fake_mcp_tool must be in conversation
    tool_msgs = [m for m in result["messages"] if isinstance(m, ToolMessage)]
    assert len(tool_msgs) == 1
    assert "result for hello" in tool_msgs[0].content


@pytest.mark.asyncio
async def test_build_graph_schema_validation_failed_when_structured_llm_raises():
    kape_cfg = make_kape_config()
    llm_cfg = make_llm_config()
    schema_cfg = SchemaConfig(
        name="test",
        json_schema={"type": "object", "properties": {"decision": {"type": "string"}}},
    )

    base_llm, _ = _build_base_llm([AIMessage(content="ok")])
    structured_ainvoke = AsyncMock(side_effect=Exception("parse error"))
    base_llm.with_structured_output = MagicMock(
        return_value=MagicMock(ainvoke=structured_ainvoke)
    )

    graph = build_graph(
        base_llm, kape_cfg, llm_cfg, schema_cfg, mcp_tools=[fake_mcp_tool]
    )
    result = await graph.ainvoke(initial_state())

    assert result["task_status"] == TaskStatus.SchemaValidationFailed
    assert result["schema_output"] is None
