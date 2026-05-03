from __future__ import annotations

from typing import Callable

from fastapi import FastAPI, Response
from prometheus_client import CONTENT_TYPE_LATEST, generate_latest


def build_probe_app(get_ready: Callable[[], bool]) -> FastAPI:
    """Build FastAPI app exposing /healthz, /readyz, and /metrics probes."""
    app = FastAPI()

    @app.get("/healthz")
    async def healthz() -> dict:
        return {"status": "ok"}

    @app.get("/readyz")
    async def readyz(response: Response) -> dict:
        if get_ready():
            return {"status": "ready"}
        response.status_code = 503
        return {"status": "not ready"}

    @app.get("/metrics")
    async def metrics() -> Response:
        return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)

    return app
