# runtime/src/kape_runtime/actions/event_emitter.py
from __future__ import annotations

import json
import uuid
from datetime import datetime, timezone
from typing import Any

from jinja2 import Environment

_PROMPT_MARKER = "$prompt"
# Templates render JSON event payloads, not HTML — autoescape would corrupt JSON
# strings containing &, <, >, ', or ". Action configs come from operator-controlled
# KapeHandler CRDs, not untrusted input.
_jinja = Environment(autoescape=False)  # noqa: S701


def _render(value: Any, ctx: dict[str, Any]) -> Any:
    """Recursively render Jinja2 strings; preserve `$prompt` marker verbatim."""
    if isinstance(value, str):
        if value == _PROMPT_MARKER:
            return _PROMPT_MARKER
        return _jinja.from_string(value).render(ctx)
    if isinstance(value, dict):
        return {k: _render(v, ctx) for k, v in value.items()}
    if isinstance(value, list):
        return [_render(v, ctx) for v in value]
    return value


async def emit_event(
    action: dict[str, Any],
    schema_output: dict[str, Any],
    parent_task_id: str,
    nats_client: Any,
) -> None:
    """Build a CloudEvent from the action config and publish it to NATS."""
    subject: str = action["subject"]
    event_type: str = action.get("event_type") or action.get("type_field") or subject
    source: str = action.get("source", "kape-runtime")
    payload_template = action.get("payload", {})

    rendered_payload = _render(payload_template, schema_output)
    if not isinstance(rendered_payload, dict):
        rendered_payload = {"value": rendered_payload}
    rendered_payload["parent_task_id"] = parent_task_id

    envelope = {
        "specversion": "1.0",
        "id": str(uuid.uuid4()),
        "type": event_type,
        "source": source,
        "time": datetime.now(tz=timezone.utc).isoformat(),
        "datacontenttype": "application/json",
        "data": rendered_payload,
    }

    await nats_client.publish(subject, json.dumps(envelope).encode("utf-8"))
