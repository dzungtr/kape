# runtime/src/kape_runtime/actions/webhook.py
from __future__ import annotations

from typing import Any

import httpx
from jinja2 import Environment

# Templates render JSON webhook bodies, not HTML — autoescape would corrupt
# JSON string contents. Templates come from operator-controlled KapeHandler CRDs.
_jinja = Environment(autoescape=False)  # noqa: S701
_TIMEOUT_SECONDS = 10.0


async def post_webhook(action: dict[str, Any], schema_output: dict[str, Any]) -> None:
    """POST a Jinja2-rendered JSON body to the configured URL."""
    url: str = action["url"]
    body_template: str = action.get("body_template", "{}")
    rendered = _jinja.from_string(body_template).render(schema_output)

    async with httpx.AsyncClient(timeout=_TIMEOUT_SECONDS) as client:
        response = await client.post(
            url,
            content=rendered,
            headers={"Content-Type": "application/json"},
        )
        response.raise_for_status()
