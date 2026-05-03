# runtime/tests/test_consumer.py
import json
import pytest
from datetime import datetime, timezone
from unittest.mock import AsyncMock, MagicMock
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
        kape_cfg=MagicMock(
            handler_name="test",
            handler_namespace="default",
            cluster_name="kind-local",
            dry_run=False,
            max_event_age_seconds=300,
            schema_name="test-schema",
        ),
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
        kape_cfg=MagicMock(
            handler_name="test",
            handler_namespace="default",
            cluster_name="kind-local",
            dry_run=False,
            max_event_age_seconds=300,
            schema_name="test-schema",
        ),
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
        kape_cfg=MagicMock(
            handler_name="test",
            handler_namespace="default",
            cluster_name="kind-local",
            dry_run=False,
            max_event_age_seconds=300,
            schema_name="test-schema",
        ),
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
        kape_cfg=MagicMock(
            handler_name="test",
            handler_namespace="default",
            cluster_name="kind-local",
            dry_run=False,
            max_event_age_seconds=300,
            schema_name="test-schema",
        ),
    )

    await loop.process_message(msg)

    update_kwargs = mock_task_svc.update_status.call_args.kwargs
    assert update_kwargs["status"] == "Failed"
    assert update_kwargs["error"]["type"] == "UnhandledError"
    assert "unexpected crash" in update_kwargs["error"]["detail"]
