from __future__ import annotations

from typing import Any

import httpx


class TaskServiceClient:
    """Async HTTP client for the kape-task-service REST API."""

    def __init__(self, endpoint: str, http: httpx.AsyncClient) -> None:
        self._base = endpoint.rstrip("/")
        self._http = http

    async def create(self, payload: dict[str, Any]) -> dict[str, Any]:
        """POST /tasks — create Task with Processing status on NATS ACK."""
        resp = await self._http.post(f"{self._base}/tasks", json=payload)
        resp.raise_for_status()
        return resp.json()

    async def update_status(
        self,
        task_id: str,
        *,
        status: str,
        completed_at: str | None = None,
        schema_output: dict[str, Any] | None = None,
        actions: list[dict[str, Any]] | None = None,
        error: dict[str, Any] | None = None,
        duration_ms: int | None = None,
        otel_trace_id: str | None = None,
    ) -> dict[str, Any]:
        """PATCH /tasks/{id}/status — write final Task status after agent completes."""
        body: dict[str, Any] = {"status": status}
        if completed_at is not None:
            body["completed_at"] = completed_at
        if schema_output is not None:
            body["schema_output"] = schema_output
        if actions is not None:
            body["actions"] = actions
        if error is not None:
            body["error"] = error
        if duration_ms is not None:
            body["duration_ms"] = duration_ms
        resp = await self._http.patch(
            f"{self._base}/tasks/{task_id}/status", json=body
        )
        resp.raise_for_status()
        return resp.json()

    async def delete(self, task_id: str) -> None:
        """DELETE /tasks/{id} — discard stale event; no terminal state written."""
        resp = await self._http.delete(f"{self._base}/tasks/{task_id}")
        resp.raise_for_status()

    async def get(self, task_id: str) -> dict[str, Any]:
        """GET /tasks/{id} — fetch a task (used by entry_router on retry path)."""
        resp = await self._http.get(f"{self._base}/tasks/{task_id}")
        resp.raise_for_status()
        return resp.json()
