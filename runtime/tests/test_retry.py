# runtime/tests/test_retry.py
import json
from datetime import datetime, timezone
from unittest.mock import AsyncMock, MagicMock

import httpx
import pytest
import tenacity

from kape_runtime.consumer import ConsumerLoop
from kape_runtime.models import TaskStatus


def _fresh_cloud_event() -> dict:
    return {
        "specversion": "1.0",
        "type": "kape.events.alertmanager",
        "source": "alertmanager",
        "id": "evt-001",
        "time": datetime.now(tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "datacontenttype": "application/json",
        "data": {"alertname": "TestAlert"},
    }


def _make_msg(event: dict) -> MagicMock:
    msg = MagicMock()
    msg.data = json.dumps(event).encode()
    msg.subject = "kape.events.alertmanager"
    msg.ack = AsyncMock()
    return msg


def _make_429() -> httpx.HTTPStatusError:
    request = httpx.Request("POST", "https://api.example.com/v1/messages")
    response = httpx.Response(status_code=429, request=request)
    return httpx.HTTPStatusError("rate limited", request=request, response=response)


def _kape_cfg() -> MagicMock:
    return MagicMock(
        handler_name="test",
        handler_namespace="default",
        cluster_name="kind-local",
        dry_run=False,
        max_event_age_seconds=300,
        schema_name="test-schema",
    )


@pytest.fixture(autouse=True)
def _patch_retry_wait(monkeypatch):
    """Patch the module-level retry decorator to use no wait between attempts."""
    from kape_runtime import consumer as consumer_mod

    fast_retry = tenacity.retry(
        retry=tenacity.retry_if_exception(consumer_mod._is_transient_llm_error),
        wait=tenacity.wait_none(),
        stop=tenacity.stop_after_attempt(5),
        reraise=True,
    )
    monkeypatch.setattr(consumer_mod, "_llm_retry", fast_retry)


@pytest.mark.asyncio
async def test_transient_429_triggers_retry_then_succeeds():
    msg = _make_msg(_fresh_cloud_event())

    mock_task_svc = AsyncMock()
    mock_task_svc.create.return_value = {"id": "01JXYZ"}

    success_state = {
        "task_status": TaskStatus.Completed,
        "schema_output": {"decision": "ignore", "confidence": 0.9, "reasoning": "OK"},
        "parse_error": None,
        "messages": [],
        "action_results": [],
        "should_abort": False,
    }
    mock_graph = AsyncMock()
    mock_graph.ainvoke.side_effect = [_make_429(), _make_429(), success_state]

    nc = AsyncMock()

    loop = ConsumerLoop(task_svc=mock_task_svc, graph=mock_graph, kape_cfg=_kape_cfg())
    loop._nc = nc

    await loop.process_message(msg)

    assert mock_graph.ainvoke.await_count == 3
    update_kwargs = mock_task_svc.update_status.call_args.kwargs
    assert update_kwargs["status"] == "Completed"
    nc.publish.assert_not_awaited()


@pytest.mark.asyncio
async def test_non_retryable_error_goes_to_dlq_immediately():
    msg = _make_msg(_fresh_cloud_event())

    mock_task_svc = AsyncMock()
    mock_task_svc.create.return_value = {"id": "01JXYZ"}

    mock_graph = AsyncMock()
    mock_graph.ainvoke.side_effect = ValueError("schema bad")

    nc = AsyncMock()

    loop = ConsumerLoop(task_svc=mock_task_svc, graph=mock_graph, kape_cfg=_kape_cfg())
    loop._nc = nc

    await loop.process_message(msg)

    # Non-retryable: graph called exactly once, then DLQ
    assert mock_graph.ainvoke.await_count == 1
    nc.publish.assert_awaited_once()
    subject, _ = nc.publish.call_args.args
    assert subject == "kape.events.dlq.test"

    update_kwargs = mock_task_svc.update_status.call_args.kwargs
    assert update_kwargs["status"] == "Failed"


@pytest.mark.asyncio
async def test_five_failed_retries_routes_to_dlq():
    msg = _make_msg(_fresh_cloud_event())

    mock_task_svc = AsyncMock()
    mock_task_svc.create.return_value = {"id": "01JXYZ"}

    mock_graph = AsyncMock()
    mock_graph.ainvoke.side_effect = [_make_429() for _ in range(5)]

    nc = AsyncMock()

    loop = ConsumerLoop(task_svc=mock_task_svc, graph=mock_graph, kape_cfg=_kape_cfg())
    loop._nc = nc

    await loop.process_message(msg)

    # 5 attempts then exhaust
    assert mock_graph.ainvoke.await_count == 5
    nc.publish.assert_awaited_once()
    subject, payload = nc.publish.call_args.args
    assert subject == "kape.events.dlq.test"
    body = json.loads(payload)
    assert "HTTPStatusError" in body["error"]

    update_kwargs = mock_task_svc.update_status.call_args.kwargs
    assert update_kwargs["status"] == "Failed"
