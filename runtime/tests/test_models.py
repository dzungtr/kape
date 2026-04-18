import pytest
from datetime import datetime, timezone
from kape_runtime.models import (
    TaskStatus,
    Task,
    TaskError,
    ActionResult,
    CloudEvent,
)


def test_task_status_values():
    assert TaskStatus.Processing == "Processing"
    assert TaskStatus.Completed == "Completed"
    assert TaskStatus.Failed == "Failed"
    assert TaskStatus.SchemaValidationFailed == "SchemaValidationFailed"
    assert TaskStatus.ActionError == "ActionError"
    assert TaskStatus.UnprocessableEvent == "UnprocessableEvent"
    assert TaskStatus.Timeout == "Timeout"
    assert TaskStatus.Retried == "Retried"


def test_cloud_event_parses_standard_fields():
    raw = {
        "specversion": "1.0",
        "type": "kape.events.alertmanager",
        "source": "alertmanager",
        "id": "abc-123",
        "time": "2026-04-18T10:00:00Z",
        "datacontenttype": "application/json",
        "data": {"alertname": "TestAlert"},
    }
    event = CloudEvent.model_validate(raw)
    assert event.id == "abc-123"
    assert event.type == "kape.events.alertmanager"
    assert isinstance(event.time, datetime)
    assert event.data == {"alertname": "TestAlert"}


def test_cloud_event_captures_extension_attributes():
    raw = {
        "specversion": "1.0",
        "type": "kape.events.alertmanager",
        "source": "alertmanager",
        "id": "abc-456",
        "time": "2026-04-18T10:00:00Z",
        "datacontenttype": "application/json",
        "data": {},
        "retry_of": "01HX...",
    }
    event = CloudEvent.model_validate(raw)
    assert event.extensions.get("retry_of") == "01HX..."


def test_action_result_construction():
    result = ActionResult(name="notify", type="webhook", status="Completed", dry_run=False)
    assert result.error is None


def test_task_error_construction():
    err = TaskError(type="UnhandledError", detail="something broke")
    assert err.schema is None
    assert err.traceback is None


def test_task_construction():
    now = datetime.now(tz=timezone.utc)
    task = Task(
        id="01JXYZ",
        cluster="kind-local",
        handler="test-handler",
        namespace="default",
        event_id="evt-001",
        event_source="alertmanager",
        event_type="kape.events.alertmanager",
        event_raw={"specversion": "1.0"},
        status=TaskStatus.Processing,
        dry_run=False,
        received_at=now,
    )
    assert task.schema_output is None
    assert task.retry_of is None
