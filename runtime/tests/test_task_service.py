import pytest
import respx
import httpx
from datetime import datetime, timezone
from kape_runtime.task_service import TaskServiceClient


BASE_URL = "http://localhost:8080"
NOW = datetime.now(tz=timezone.utc)

CREATE_PAYLOAD = {
    "id": "01JXYZ",
    "cluster": "kind-local",
    "handler": "test-handler",
    "namespace": "default",
    "event_id": "evt-001",
    "event_source": "alertmanager",
    "event_type": "kape.events.alertmanager",
    "event_raw": {"specversion": "1.0"},
    "status": "Processing",
    "dry_run": False,
    "received_at": NOW.isoformat(),
}

TASK_RESPONSE = {
    **CREATE_PAYLOAD,
    "schema_output": None,
    "actions": None,
    "error": None,
    "retry_of": None,
    "otel_trace_id": None,
    "completed_at": None,
    "duration_ms": None,
}


@pytest.mark.asyncio
async def test_create_task_posts_to_tasks_endpoint():
    async with respx.mock:
        route = respx.post(f"{BASE_URL}/tasks").mock(
            return_value=httpx.Response(201, json=TASK_RESPONSE)
        )
        async with httpx.AsyncClient() as http:
            client = TaskServiceClient(BASE_URL, http)
            task = await client.create(CREATE_PAYLOAD)

    assert route.called
    assert task["id"] == "01JXYZ"
    assert task["status"] == "Processing"


@pytest.mark.asyncio
async def test_update_status_patches_task_status_endpoint():
    task_id = "01JXYZ"
    updated = {**TASK_RESPONSE, "status": "Completed", "duration_ms": 1200}

    async with respx.mock:
        route = respx.patch(f"{BASE_URL}/tasks/{task_id}/status").mock(
            return_value=httpx.Response(200, json=updated)
        )
        async with httpx.AsyncClient() as http:
            client = TaskServiceClient(BASE_URL, http)
            result = await client.update_status(task_id, status="Completed", duration_ms=1200)

    assert route.called
    assert result["status"] == "Completed"


@pytest.mark.asyncio
async def test_delete_task_calls_delete_endpoint():
    task_id = "01JXYZ"

    async with respx.mock:
        route = respx.delete(f"{BASE_URL}/tasks/{task_id}").mock(
            return_value=httpx.Response(204)
        )
        async with httpx.AsyncClient() as http:
            client = TaskServiceClient(BASE_URL, http)
            await client.delete(task_id)

    assert route.called


@pytest.mark.asyncio
async def test_get_task_fetches_single_task():
    task_id = "01JXYZ"

    async with respx.mock:
        route = respx.get(f"{BASE_URL}/tasks/{task_id}").mock(
            return_value=httpx.Response(200, json=TASK_RESPONSE)
        )
        async with httpx.AsyncClient() as http:
            client = TaskServiceClient(BASE_URL, http)
            task = await client.get(task_id)

    assert route.called
    assert task["id"] == "01JXYZ"


@pytest.mark.asyncio
async def test_update_status_includes_schema_output():
    task_id = "01JXYZ"
    schema_out = {"decision": "ignore", "confidence": 0.9, "reasoning": "OK"}
    updated = {**TASK_RESPONSE, "status": "Completed", "schema_output": schema_out}

    async with respx.mock:
        respx.patch(f"{BASE_URL}/tasks/{task_id}/status").mock(
            return_value=httpx.Response(200, json=updated)
        )
        async with httpx.AsyncClient() as http:
            client = TaskServiceClient(BASE_URL, http)
            result = await client.update_status(
                task_id,
                status="Completed",
                schema_output=schema_out,
            )

    assert result["schema_output"] == schema_out
