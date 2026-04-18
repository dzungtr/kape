from __future__ import annotations

from datetime import datetime
from enum import Enum
from typing import Any

from pydantic import BaseModel, ConfigDict, Field, model_validator


class TaskStatus(str, Enum):
    Processing = "Processing"
    Completed = "Completed"
    Failed = "Failed"
    SchemaValidationFailed = "SchemaValidationFailed"
    ActionError = "ActionError"
    UnprocessableEvent = "UnprocessableEvent"
    PendingApproval = "PendingApproval"
    Timeout = "Timeout"
    Retried = "Retried"


class ActionResult(BaseModel):
    name: str
    type: str
    status: str
    dry_run: bool
    error: str | None = None


class TaskError(BaseModel):
    type: str
    detail: str
    schema: str | None = None
    raw: str | None = None
    traceback: str | None = None


class Task(BaseModel):
    id: str
    cluster: str
    handler: str
    namespace: str
    event_id: str
    event_source: str
    event_type: str
    event_raw: dict[str, Any]
    status: TaskStatus
    dry_run: bool
    schema_output: dict[str, Any] | None = None
    actions: list[ActionResult] | None = None
    error: TaskError | None = None
    retry_of: str | None = None
    otel_trace_id: str | None = None
    received_at: datetime
    completed_at: datetime | None = None
    duration_ms: int | None = None


_KNOWN_CE_FIELDS = {
    "specversion", "type", "source", "id", "time",
    "datacontenttype", "data",
}


class CloudEvent(BaseModel):
    model_config = ConfigDict(extra="allow")

    specversion: str
    type: str
    source: str
    id: str
    time: datetime
    datacontenttype: str = "application/json"
    data: dict[str, Any]
    extensions: dict[str, Any] = Field(default_factory=dict)

    @model_validator(mode="before")
    @classmethod
    def _extract_extensions(cls, values: dict) -> dict:
        extensions = {
            k: v for k, v in values.items() if k not in _KNOWN_CE_FIELDS
        }
        values["extensions"] = extensions
        return values
