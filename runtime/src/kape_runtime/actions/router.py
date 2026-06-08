# runtime/src/kape_runtime/actions/router.py
from __future__ import annotations

import logging
from typing import Any

from jsonpath_ng.ext import parse as jsonpath_parse

from kape_runtime.actions.event_emitter import emit_event
from kape_runtime.actions.save_memory import save_memory
from kape_runtime.actions.webhook import post_webhook
from kape_runtime.metrics import kape_action_results_total

logger = logging.getLogger(__name__)


def _condition_matches(condition: str | None, schema_output: dict[str, Any]) -> bool:
    """Empty/missing condition is truthy; otherwise evaluate JSONPath against schema_output."""
    if not condition:
        return True
    try:
        expr = jsonpath_parse(condition)
        matches = expr.find(schema_output)
        return any(m.value for m in matches)
    except Exception as exc:
        logger.warning("Invalid JSONPath condition %r: %s", condition, exc)
        return False


async def run_actions(
    schema_output: dict[str, Any],
    actions: list[dict[str, Any]],
    nats_client: Any,
    qdrant_client: Any,
    handler_name: str,
    task_id: str,
    parent_task_id: str,
) -> list[dict[str, Any]]:
    """Run each configured action against schema_output; collect results, never raise."""
    results: list[dict[str, Any]] = []

    for action in actions:
        name = action.get("name", "<unnamed>")
        action_type = action.get("type", "")

        if not _condition_matches(action.get("condition"), schema_output):
            result = {"name": name, "type": action_type, "status": "skipped"}
            results.append(result)
            kape_action_results_total.labels(
                handler=handler_name, action=name, type=action_type, status="skipped"
            ).inc()
            continue

        try:
            if action_type == "event-publish":
                await emit_event(
                    action=action,
                    schema_output=schema_output,
                    parent_task_id=parent_task_id,
                    nats_client=nats_client,
                )
            elif action_type == "webhook":
                await post_webhook(action=action, schema_output=schema_output)
            elif action_type == "save_memory":
                await save_memory(
                    action=action,
                    schema_output=schema_output,
                    qdrant_client=qdrant_client,
                    handler_name=handler_name,
                    task_id=task_id,
                )
            else:
                result = {
                    "name": name,
                    "type": action_type,
                    "status": "error",
                    "error": f"unknown action type: {action_type!r}",
                }
                results.append(result)
                kape_action_results_total.labels(
                    handler=handler_name, action=name, type=action_type, status="error"
                ).inc()
                continue

            results.append({"name": name, "type": action_type, "status": "success"})
            kape_action_results_total.labels(
                handler=handler_name, action=name, type=action_type, status="success"
            ).inc()
        except Exception as exc:
            logger.exception("Action %r (%s) failed", name, action_type)
            result = {
                "name": name,
                "type": action_type,
                "status": "error",
                "error": str(exc),
            }
            results.append(result)
            kape_action_results_total.labels(
                handler=handler_name, action=name, type=action_type, status="error"
            ).inc()

    return results
