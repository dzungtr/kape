# runtime/tests/test_actions.py
import json
from unittest.mock import AsyncMock, MagicMock

import httpx
import pytest

from kape_runtime.actions.event_emitter import emit_event
from kape_runtime.actions.router import run_actions
from kape_runtime.actions.save_memory import save_memory
from kape_runtime.actions.webhook import post_webhook


@pytest.mark.asyncio
async def test_event_publish_action_publishes_cloudevent_to_nats():
    schema_output = {
        "decision": "change-required",
        "reasoning": "scale up needed",
        "nodepool": "default",
    }
    action = {
        "name": "request-gitops-pr",
        "type": "event-publish",
        "subject": "kape.events.gitops.pr-requested",
        "source": "kape.handlers.karpenter",
        "event_type": "kape.events.gitops.pr-requested",
        "payload": {
            "nodepool": "{{ nodepool }}",
            "reasoning": "{{ reasoning }}",
            "prompt": "$prompt",
        },
    }
    nats_client = MagicMock()
    nats_client.publish = AsyncMock()

    await emit_event(
        action=action,
        schema_output=schema_output,
        parent_task_id="01PARENT",
        nats_client=nats_client,
    )

    nats_client.publish.assert_awaited_once()
    args, kwargs = nats_client.publish.call_args
    subject = args[0] if args else kwargs.get("subject")
    payload_bytes = args[1] if len(args) > 1 else kwargs.get("payload")
    assert subject == "kape.events.gitops.pr-requested"

    envelope = json.loads(payload_bytes)
    assert envelope["specversion"] == "1.0"
    assert envelope["type"] == "kape.events.gitops.pr-requested"
    assert envelope["source"] == "kape.handlers.karpenter"
    assert "id" in envelope and envelope["id"]
    assert envelope["data"]["parent_task_id"] == "01PARENT"
    assert envelope["data"]["nodepool"] == "default"
    assert envelope["data"]["reasoning"] == "scale up needed"
    # $prompt passthrough is preserved literally
    assert envelope["data"]["prompt"] == "$prompt"


@pytest.mark.asyncio
async def test_webhook_action_posts_rendered_body(respx_mock):
    schema_output = {"severity": "high", "service": "mock-api"}
    body_template = '{"severity": "{{ severity }}", "service": "{{ service }}"}'
    action = {
        "name": "notify-sre",
        "type": "webhook",
        "url": "http://example.test/webhook",
        "body_template": body_template,
    }
    route = respx_mock.post("http://example.test/webhook").mock(
        return_value=httpx.Response(200)
    )

    await post_webhook(action=action, schema_output=schema_output)

    assert route.called
    request = route.calls.last.request
    body = json.loads(request.content)
    assert body == {"severity": "high", "service": "mock-api"}


@pytest.mark.asyncio
async def test_save_memory_action_upserts_to_qdrant():
    schema_output = {"decision": "scale-up", "summary": "burst expected"}
    action = {
        "name": "remember-decision",
        "type": "save_memory",
        "collection": "decisions",
        "text_template": "Decision: {{ decision }} — {{ summary }}",
    }
    qdrant_client = MagicMock()
    qdrant_client.upsert = AsyncMock()

    await save_memory(
        action=action,
        schema_output=schema_output,
        qdrant_client=qdrant_client,
        handler_name="karpenter-handler",
        task_id="01TASK",
    )

    qdrant_client.upsert.assert_awaited_once()
    _, kwargs = qdrant_client.upsert.call_args
    assert kwargs["collection_name"] == "decisions"
    points = kwargs["points"]
    assert len(points) == 1
    payload = points[0]["payload"]
    assert payload["text"] == "Decision: scale-up — burst expected"
    assert payload["handler_name"] == "karpenter-handler"
    assert payload["task_id"] == "01TASK"
    assert "timestamp" in payload and payload["timestamp"]


@pytest.mark.asyncio
async def test_action_failure_does_not_stop_remaining(respx_mock):
    schema_output = {"value": 42}
    failing_action = {
        "name": "bad-webhook",
        "type": "webhook",
        "url": "http://example.test/bad",
        "body_template": '{"value": {{ value }}}',
    }
    good_action = {
        "name": "good-webhook",
        "type": "webhook",
        "url": "http://example.test/good",
        "body_template": '{"value": {{ value }}}',
    }
    respx_mock.post("http://example.test/bad").mock(
        return_value=httpx.Response(500, text="boom")
    )
    good_route = respx_mock.post("http://example.test/good").mock(
        return_value=httpx.Response(200)
    )

    results = await run_actions(
        schema_output=schema_output,
        actions=[failing_action, good_action],
        nats_client=None,
        qdrant_client=None,
        handler_name="h",
        task_id="t",
        parent_task_id="p",
    )

    assert good_route.called
    assert len(results) == 2
    assert results[0]["status"] == "error"
    assert results[1]["status"] == "success"


@pytest.mark.asyncio
async def test_jsonpath_condition_false_skips_action():
    schema_output = {"decision": "ignore"}
    action = {
        "name": "conditional-webhook",
        "type": "webhook",
        "condition": "$.decision[?(@ == 'change-required')]",
        "url": "http://example.test/should-not-call",
        "body_template": "{}",
    }

    # No respx mock set up — if the handler ran, httpx would raise a connect error.
    results = await run_actions(
        schema_output=schema_output,
        actions=[action],
        nats_client=None,
        qdrant_client=None,
        handler_name="h",
        task_id="t",
        parent_task_id="p",
    )

    assert len(results) == 1
    assert results[0]["status"] == "skipped"
