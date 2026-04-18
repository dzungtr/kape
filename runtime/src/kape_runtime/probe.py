from __future__ import annotations

from typing import Callable

from fastapi import FastAPI, Response


def build_probe_app(get_ready: Callable[[], bool]) -> FastAPI:
    """Build FastAPI app exposing /healthz and /readyz probes."""
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

    return app
