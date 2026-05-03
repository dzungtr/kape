# runtime/tests/test_dlq.py
import base64
import json

import pytest
from unittest.mock import AsyncMock

from kape_runtime.dlq import publish_dlq


@pytest.mark.asyncio
async def test_publish_dlq_writes_envelope_to_per_handler_subject():
    nc = AsyncMock()
    raw = b'{"id":"evt-1"}'
    exc = RuntimeError("boom")

    await publish_dlq(nc, "alertmanager", "task-123", raw, exc)

    nc.publish.assert_awaited_once()
    subject, payload = nc.publish.call_args.args
    assert subject == "kape.events.dlq.alertmanager"

    body = json.loads(payload)
    assert base64.b64decode(body["original_event"]) == raw
    assert body["error"] == "RuntimeError: boom"
    assert body["task_id"] == "task-123"
    assert body["handler"] == "alertmanager"
    assert "timestamp" in body


@pytest.mark.asyncio
async def test_publish_dlq_swallows_publish_failures():
    nc = AsyncMock()
    nc.publish.side_effect = ConnectionError("nats down")

    # Should not raise — DLQ failures must not crash the consumer
    await publish_dlq(nc, "h", "t", b"{}", RuntimeError("orig"))
    nc.publish.assert_awaited_once()
