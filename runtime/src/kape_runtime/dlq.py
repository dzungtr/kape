# runtime/src/kape_runtime/dlq.py
from __future__ import annotations

import base64
import json
import logging
from datetime import datetime, timezone
from typing import Any

logger = logging.getLogger(__name__)


async def publish_dlq(
    nats_client: Any,
    handler_name: str,
    task_id: str,
    original_event: bytes,
    exc: BaseException,
) -> None:
    """Publish a failed-event payload to the per-handler DLQ subject."""
    subject = f"kape.events.dlq.{handler_name}"
    body = {
        "original_event": base64.b64encode(original_event).decode(),
        "error": f"{type(exc).__name__}: {exc}",
        "task_id": task_id,
        "handler": handler_name,
        "timestamp": datetime.now(tz=timezone.utc).isoformat(),
    }
    payload = json.dumps(body).encode()
    try:
        await nats_client.publish(subject, payload)
    except Exception:
        logger.exception("Failed to publish DLQ message for task %s", task_id)
