# runtime/src/kape_runtime/main.py
from __future__ import annotations

import asyncio
import logging
import signal

import httpx
import uvicorn

from kape_runtime.config import build_llm, load_config
from kape_runtime.consumer import ConsumerLoop
from kape_runtime.graph.graph import build_graph
from kape_runtime.probe import build_probe_app
from kape_runtime.task_service import TaskServiceClient
from kape_runtime.tracing import setup_tracing

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


async def main() -> None:
    config = load_config()

    setup_tracing(
        config=config.otel,
        handler=config.kape.handler_name,
        cluster=config.kape.cluster_name,
        namespace=config.kape.handler_namespace,
    )

    llm = build_llm(config.llm)
    compiled_graph = build_graph(llm, config.kape, config.llm, config.schema)

    _ready = False

    async with httpx.AsyncClient(timeout=10.0) as http:
        task_svc = TaskServiceClient(config.task_service.endpoint, http)
        consumer = ConsumerLoop(
            task_svc=task_svc,
            graph=compiled_graph,
            kape_cfg=config.kape,
        )

        probe_app = build_probe_app(get_ready=lambda: _ready)
        probe_cfg = uvicorn.Config(probe_app, host="0.0.0.0", port=8080, log_level="warning")
        probe_server = uvicorn.Server(probe_cfg)

        def _shutdown(signum, frame):
            logger.info("Received signal %s — shutting down", signum)
            probe_server.should_exit = True

        signal.signal(signal.SIGTERM, _shutdown)
        signal.signal(signal.SIGINT, _shutdown)

        probe_task = asyncio.create_task(probe_server.serve())

        logger.info(
            "Handler %s starting; connecting to NATS %s",
            config.kape.handler_name,
            config.nats.url,
        )

        _ready = True

        try:
            await consumer.run(config.nats)
        finally:
            probe_server.should_exit = True
            await probe_task


def entrypoint() -> None:
    asyncio.run(main())


if __name__ == "__main__":
    entrypoint()
