# runtime/src/kape_runtime/actions/save_memory.py
from __future__ import annotations

import uuid
from datetime import datetime, timezone
from typing import Any

from jinja2 import Environment

# Templates render plain memory text for Qdrant, not HTML — autoescape would
# corrupt human-readable text. Templates come from operator-controlled CRDs.
_jinja = Environment(autoescape=False)  # noqa: S701


async def save_memory(
    action: dict[str, Any],
    schema_output: dict[str, Any],
    qdrant_client: Any,
    handler_name: str,
    task_id: str,
) -> None:
    """Render the text template and upsert one document into a Qdrant collection."""
    collection_name: str = action.get("collection", "kape_memory")
    text_template: str = action.get("text_template", "")
    rendered_text = _jinja.from_string(text_template).render(schema_output)

    point = {
        "id": str(uuid.uuid4()),
        "payload": {
            "text": rendered_text,
            "handler_name": handler_name,
            "task_id": task_id,
            "timestamp": datetime.now(tz=timezone.utc).isoformat(),
        },
    }

    await qdrant_client.upsert(collection_name=collection_name, points=[point])
