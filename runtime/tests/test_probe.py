import pytest
from httpx import AsyncClient, ASGITransport
from kape_runtime.probe import build_probe_app


@pytest.mark.asyncio
async def test_healthz_always_returns_200():
    app = build_probe_app(get_ready=lambda: False)
    async with AsyncClient(transport=ASGITransport(app=app), base_url="http://test") as client:
        resp = await client.get("/healthz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


@pytest.mark.asyncio
async def test_readyz_returns_200_when_ready():
    app = build_probe_app(get_ready=lambda: True)
    async with AsyncClient(transport=ASGITransport(app=app), base_url="http://test") as client:
        resp = await client.get("/readyz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ready"}


@pytest.mark.asyncio
async def test_readyz_returns_503_when_not_ready():
    app = build_probe_app(get_ready=lambda: False)
    async with AsyncClient(transport=ASGITransport(app=app), base_url="http://test") as client:
        resp = await client.get("/readyz")
    assert resp.status_code == 503
    assert resp.json() == {"status": "not ready"}
