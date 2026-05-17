# runtime/tests/test_consumer.py
import json
import pytest
from datetime import datetime, timezone
from unittest.mock import AsyncMock, MagicMock, patch
from kape_runtime.consumer import ConsumerLoop
from kape_runtime.models import TaskStatus


def make_nats_msg(data: dict, subject: str = "kape.events.alertmanager") -> MagicMock:
    msg = MagicMock()
    msg.data = json.dumps(data).encode()
    msg.subject = subject
    msg.ack = AsyncMock()
    return msg


def _fresh_cloud_event() -> dict:
    """Return a CloudEvent dict with a current timestamp to avoid staleness check."""
    return {
        "specversion": "1.0",
        "type": "kape.events.alertmanager",
        "source": "alertmanager",
        "id": "evt-001",
        "time": datetime.now(tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "datacontenttype": "application/json",
        "data": {"alertname": "TestAlert"},
    }


def _make_kape_cfg(**overrides) -> MagicMock:
    """Create a KapeConfig mock with safe defaults (no actions by default)."""
    defaults = dict(
        handler_name="test",
        handler_namespace="default",
        cluster_name="kind-local",
        dry_run=False,
        max_event_age_seconds=300,
        schema_name="test-schema",
        actions=[],
    )
    defaults.update(overrides)
    return MagicMock(**defaults)


CLOUD_EVENT = _fresh_cloud_event()


@pytest.mark.asyncio
async def test_consumer_acks_message_before_processing():
    msg = make_nats_msg(CLOUD_EVENT)

    mock_task_svc = AsyncMock()
    mock_task_svc.create.return_value = {"id": "01JXYZ"}
    mock_task_svc.update_status = AsyncMock()

    mock_graph = AsyncMock()
    mock_graph.ainvoke.return_value = {
        "task_status": TaskStatus.Completed,
        "schema_output": {"decision": "ignore", "confidence": 0.9, "reasoning": "OK"},
        "parse_error": None,
        "messages": [],
        "action_results": [],
        "should_abort": False,
    }

    loop = ConsumerLoop(
        task_svc=mock_task_svc,
        graph=mock_graph,
        kape_cfg=_make_kape_cfg(),
    )

    await loop.process_message(msg)

    msg.ack.assert_awaited_once()
    mock_task_svc.create.assert_awaited_once()
    create_payload = mock_task_svc.create.call_args[0][0]
    assert create_payload["status"] == "Processing"
    mock_task_svc.update_status.assert_awaited_once()
    update_kwargs = mock_task_svc.update_status.call_args.kwargs
    assert update_kwargs["status"] == "Completed"


@pytest.mark.asyncio
async def test_consumer_marks_unprocessable_on_bad_json():
    msg = MagicMock()
    msg.data = b"not-json"
    msg.subject = "kape.events.alertmanager"
    msg.ack = AsyncMock()

    mock_task_svc = AsyncMock()
    mock_task_svc.create.return_value = {"id": "01JXYZ"}
    mock_task_svc.update_status = AsyncMock()

    loop = ConsumerLoop(
        task_svc=mock_task_svc,
        graph=AsyncMock(),
        kape_cfg=_make_kape_cfg(),
    )

    await loop.process_message(msg)

    msg.ack.assert_awaited_once()
    update_kwargs = mock_task_svc.update_status.call_args.kwargs
    assert update_kwargs["status"] == "UnprocessableEvent"


@pytest.mark.asyncio
async def test_consumer_deletes_task_on_stale_event():
    stale_event = {**CLOUD_EVENT, "time": "2020-01-01T00:00:00Z"}
    msg = make_nats_msg(stale_event)

    mock_task_svc = AsyncMock()
    mock_task_svc.create.return_value = {"id": "01JXYZ"}
    mock_task_svc.delete = AsyncMock()

    loop = ConsumerLoop(
        task_svc=mock_task_svc,
        graph=AsyncMock(),
        kape_cfg=_make_kape_cfg(),
    )

    await loop.process_message(msg)

    msg.ack.assert_awaited_once()
    mock_task_svc.create.assert_awaited_once()
    mock_task_svc.delete.assert_awaited_once_with("01JXYZ")


@pytest.mark.asyncio
async def test_consumer_writes_failed_on_unhandled_exception():
    msg = make_nats_msg(CLOUD_EVENT)

    mock_task_svc = AsyncMock()
    mock_task_svc.create.return_value = {"id": "01JXYZ"}
    mock_task_svc.update_status = AsyncMock()

    mock_graph = AsyncMock()
    mock_graph.ainvoke.side_effect = RuntimeError("unexpected crash")

    loop = ConsumerLoop(
        task_svc=mock_task_svc,
        graph=mock_graph,
        kape_cfg=_make_kape_cfg(),
    )

    await loop.process_message(msg)

    update_kwargs = mock_task_svc.update_status.call_args.kwargs
    assert update_kwargs["status"] == "Failed"
    assert update_kwargs["error"]["type"] == "UnhandledError"
    assert "unexpected crash" in update_kwargs["error"]["detail"]


# --- Actions router wiring tests ---

@pytest.mark.asyncio
async def test_consumer_dispatches_actions_after_graph_completion():
    """run_actions is called after graph completes when actions are configured."""
    action_cfg = {
        "name": "notify-webhook",
        "type": "webhook",
        "url": "http://example.test/hook",
        "body_template": '{"decision": "{{ decision }}"}',
    }
    schema_output = {"decision": "ignore", "confidence": 0.9, "reasoning": "all good"}

    msg = make_nats_msg(CLOUD_EVENT)

    mock_task_svc = AsyncMock()
    mock_task_svc.create.return_value = {"id": "01TASK"}
    mock_task_svc.update_status = AsyncMock()

    mock_graph = AsyncMock()
    mock_graph.ainvoke.return_value = {
        "task_status": TaskStatus.Completed,
        "schema_output": schema_output,
        "parse_error": None,
        "messages": [],
        "action_results": [],
        "should_abort": False,
    }

    fake_action_results = [{"name": "notify-webhook", "type": "webhook", "status": "success"}]

    with patch("kape_runtime.consumer.run_actions", new=AsyncMock(return_value=fake_action_results)) as mock_run_actions:
        loop = ConsumerLoop(
            task_svc=mock_task_svc,
            graph=mock_graph,
            kape_cfg=_make_kape_cfg(actions=[action_cfg]),
        )
        await loop.process_message(msg)

    mock_run_actions.assert_awaited_once()
    call_kwargs = mock_run_actions.call_args.kwargs
    assert call_kwargs["schema_output"] == schema_output
    assert call_kwargs["actions"] == [action_cfg]
    assert call_kwargs["handler_name"] == "test"
    assert call_kwargs["task_id"] == "01TASK"

    # The second update_status call should include the action results
    assert mock_task_svc.update_status.await_count == 2
    second_call_kwargs = mock_task_svc.update_status.call_args_list[1].kwargs
    assert second_call_kwargs["actions"] == fake_action_results
    assert second_call_kwargs["status"] == "Completed"


@pytest.mark.asyncio
async def test_consumer_skips_actions_when_schema_output_is_none():
    """Actions are NOT dispatched when schema_output is None (parse failure path)."""
    msg = make_nats_msg(CLOUD_EVENT)

    mock_task_svc = AsyncMock()
    mock_task_svc.create.return_value = {"id": "01TASK"}
    mock_task_svc.update_status = AsyncMock()

    mock_graph = AsyncMock()
    mock_graph.ainvoke.return_value = {
        "task_status": TaskStatus.SchemaValidationFailed,
        "schema_output": None,
        "parse_error": "LLM refused to produce JSON",
        "messages": [],
        "action_results": [],
        "should_abort": True,
    }

    action_cfg = {"name": "noop", "type": "webhook", "url": "http://x/y", "body_template": "{}"}

    with patch("kape_runtime.consumer.run_actions", new=AsyncMock()) as mock_run_actions:
        loop = ConsumerLoop(
            task_svc=mock_task_svc,
            graph=mock_graph,
            kape_cfg=_make_kape_cfg(actions=[action_cfg]),
        )
        await loop.process_message(msg)

    mock_run_actions.assert_not_awaited()


@pytest.mark.asyncio
async def test_consumer_skips_actions_when_no_actions_configured():
    """Actions are NOT dispatched when kape_cfg.actions is empty."""
    schema_output = {"decision": "ignore", "confidence": 0.9, "reasoning": "OK"}
    msg = make_nats_msg(CLOUD_EVENT)

    mock_task_svc = AsyncMock()
    mock_task_svc.create.return_value = {"id": "01TASK"}
    mock_task_svc.update_status = AsyncMock()

    mock_graph = AsyncMock()
    mock_graph.ainvoke.return_value = {
        "task_status": TaskStatus.Completed,
        "schema_output": schema_output,
        "parse_error": None,
        "messages": [],
        "action_results": [],
        "should_abort": False,
    }

    with patch("kape_runtime.consumer.run_actions", new=AsyncMock()) as mock_run_actions:
        loop = ConsumerLoop(
            task_svc=mock_task_svc,
            graph=mock_graph,
            kape_cfg=_make_kape_cfg(actions=[]),  # no actions
        )
        await loop.process_message(msg)

    mock_run_actions.assert_not_awaited()
    # Only one update_status call — no second call for actions
    mock_task_svc.update_status.assert_awaited_once()


@pytest.mark.asyncio
async def test_consumer_propagates_parent_task_id_from_event_extension():
    """parent_task_id is extracted from CloudEvent extensions and passed to run_actions."""
    event_with_parent = {
        **CLOUD_EVENT,
        "parent_task_id": "PARENT-01",
    }
    msg = make_nats_msg(event_with_parent)

    mock_task_svc = AsyncMock()
    mock_task_svc.create.return_value = {"id": "01TASK"}
    mock_task_svc.update_status = AsyncMock()

    mock_graph = AsyncMock()
    mock_graph.ainvoke.return_value = {
        "task_status": TaskStatus.Completed,
        "schema_output": {"decision": "investigate", "confidence": 0.8, "reasoning": "chain"},
        "parse_error": None,
        "messages": [],
        "action_results": [],
        "should_abort": False,
    }

    action_cfg = {"name": "chain-event", "type": "event-publish", "subject": "kape.out"}

    with patch("kape_runtime.consumer.run_actions", new=AsyncMock(return_value=[])) as mock_run_actions:
        loop = ConsumerLoop(
            task_svc=mock_task_svc,
            graph=mock_graph,
            kape_cfg=_make_kape_cfg(actions=[action_cfg]),
        )
        await loop.process_message(msg)

    call_kwargs = mock_run_actions.call_args.kwargs
    assert call_kwargs["parent_task_id"] == "PARENT-01"
