FROM python:3.12-slim AS builder
RUN pip install uv
WORKDIR /app
COPY runtime/pyproject.toml runtime/uv.lock ./
RUN uv sync --no-dev

FROM python:3.12-slim
WORKDIR /app
COPY --from=builder /app/.venv /app/.venv
COPY runtime/src ./src
ENV PATH="/app/.venv/bin:$PATH"
ENTRYPOINT ["kape-runtime"]
