# runtime/src/kape_runtime/consumer.py
from __future__ import annotations

import json
import logging
import traceback
from datetime import datetime, timezone
from typing import Any

import nats
from ulid import ULID

from kape_runtime.config import KapeConfig, NatsConfig
from kape_runtime.dedup import DedupWindow
from kape_runtime.metrics import (
    kape_decisions_total,
    kape_events_total,
    kape_llm_latency_seconds,
)
from kape_runtime.models import CloudEvent, TaskStatus
from kape_runtime.task_service import TaskServiceClient

logger = logging.getLogger(__name__)


class ConsumerLoop:
    """NATS JetStream pull consumer: one event at a time, explicit ACK-first strategy."""

    def __init__(
        self,
        task_svc: TaskServiceClient,
        graph: Any,
        kape_cfg: KapeConfig,
        dedup: DedupWindow | None = None,
    ) -> None:
        self._task_svc = task_svc
        self._graph = graph
        self._kape_cfg = kape_cfg
        self._dedup = dedup if dedup is not None else DedupWindow(ttl_seconds=60)

    async def process_message(self, msg: Any) -> None:
        """ACK → create Task → parse CloudEvent → staleness check → invoke graph → update Task."""
        kape_cfg = self._kape_cfg
        task_id = str(ULID())
        now = datetime.now(tz=timezone.utc)

        # 1. ACK immediately — prevents other replicas from receiving this message
        await msg.ack()

        # 2. Create Task with Processing status (before parse, so there is always a record)
        raw_bytes: bytes = msg.data
        create_payload: dict[str, Any] = {
            "id": task_id,
            "cluster": kape_cfg.cluster_name,
            "handler": kape_cfg.handler_name,
            "namespace": kape_cfg.handler_namespace,
            "event_id": "unknown",
            "event_source": "unknown",
            "event_type": msg.subject,
            "event_raw": {},
            "status": "Processing",
            "dry_run": kape_cfg.dry_run,
            "received_at": now.isoformat(),
        }

        # 3. Parse CloudEvent envelope
        try:
            raw_dict = json.loads(raw_bytes)
            event = CloudEvent.model_validate(raw_dict)
            create_payload["event_id"] = event.id
            create_payload["event_source"] = event.source
            create_payload["event_type"] = event.type
            create_payload["event_raw"] = raw_dict
        except Exception as parse_exc:
            logger.warning("Malformed CloudEvent: %s", parse_exc)
            task = await self._task_svc.create(create_payload)
            await self._task_svc.update_status(
                task["id"],
                status="UnprocessableEvent",
                error={
                    "type": "MalformedEvent",
                    "detail": "Could not parse CloudEvents envelope",
                    "raw": raw_bytes.decode("utf-8", errors="replace"),
                },
            )
            kape_events_total.labels(
                handler=kape_cfg.handler_name, status="failed"
            ).inc()
            return

        # 3a. Dedup check — drop duplicates seen within the dedup window without
        # creating a Task. The NATS message has already been ACKed above.
        if self._dedup.is_duplicate(event.id):
            logger.info("Dropping duplicate event %s", event.id)
            kape_events_total.labels(
                handler=kape_cfg.handler_name, status="deduplicated"
            ).inc()
            return

        task = await self._task_svc.create(create_payload)

        # 4. Staleness check — delete task silently if event is too old
        age_seconds = (datetime.now(tz=timezone.utc) - event.time).total_seconds()
        if age_seconds > kape_cfg.max_event_age_seconds:
            logger.info("Dropping stale event %s (age %.0fs)", event.id, age_seconds)
            await self._task_svc.delete(task["id"])
            return

        # 5. Invoke LangGraph agent
        from kape_runtime.graph.state import AgentState

        start_time = datetime.now(tz=timezone.utc)
        try:
            with kape_llm_latency_seconds.labels(handler=kape_cfg.handler_name).time():
                state = await self._graph.ainvoke(
                    AgentState(
                        event=raw_dict,
                        task_id=task["id"],
                        retry_task=None,
                        messages=[],
                        schema_output=None,
                        parse_error=None,
                        action_results=[],
                        task_status=None,
                        should_abort=False,
                        dry_run=kape_cfg.dry_run,
                    )
                )

            duration_ms = int(
                (datetime.now(tz=timezone.utc) - start_time).total_seconds() * 1000
            )
            task_status: TaskStatus = state["task_status"]

            update_kwargs: dict[str, Any] = {
                "status": task_status.value,
                "completed_at": datetime.now(tz=timezone.utc).isoformat(),
                "duration_ms": duration_ms,
            }
            schema_output = state.get("schema_output")
            if schema_output is not None:
                update_kwargs["schema_output"] = schema_output
                decision = (
                    schema_output.get("decision")
                    if isinstance(schema_output, dict)
                    else None
                )
                if decision:
                    kape_decisions_total.labels(
                        handler=kape_cfg.handler_name, decision=str(decision)
                    ).inc()
            if state.get("parse_error"):
                update_kwargs["error"] = {
                    "type": "SchemaValidationFailed",
                    "detail": state["parse_error"],
                    "schema": kape_cfg.schema_name,
                }

            await self._task_svc.update_status(task["id"], **update_kwargs)

            metric_status = (
                "failed" if task_status == TaskStatus.Failed else "processed"
            )
            kape_events_total.labels(
                handler=kape_cfg.handler_name, status=metric_status
            ).inc()

        except Exception as exc:
            logger.exception("Unhandled error processing event %s", event.id)
            duration_ms = int(
                (datetime.now(tz=timezone.utc) - start_time).total_seconds() * 1000
            )
            await self._task_svc.update_status(
                task["id"],
                status="Failed",
                completed_at=datetime.now(tz=timezone.utc).isoformat(),
                duration_ms=duration_ms,
                error={
                    "type": "UnhandledError",
                    "detail": str(exc),
                    "traceback": traceback.format_exc(),
                },
            )
            kape_events_total.labels(
                handler=kape_cfg.handler_name, status="failed"
            ).inc()

    async def run(self, nats_cfg: NatsConfig) -> None:
        """Connect to NATS and run the pull consumer loop indefinitely."""
        nc = await nats.connect(nats_cfg.url)
        js = nc.jetstream()
        sub = await js.pull_subscribe(
            subject=nats_cfg.subject,
            durable=nats_cfg.consumer,
            stream=nats_cfg.stream,
        )
        logger.info(
            "NATS consumer started: subject=%s consumer=%s",
            nats_cfg.subject,
            nats_cfg.consumer,
        )

        try:
            while True:
                try:
                    msgs = await sub.fetch(1, timeout=5.0)
                    for msg in msgs:
                        await self.process_message(msg)
                except nats.errors.TimeoutError:
                    continue
        finally:
            await nc.drain()
