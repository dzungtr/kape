# runtime/src/kape_runtime/config.py
from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Any

from dynaconf import Dynaconf


@dataclass
class KapeConfig:
    handler_name: str
    handler_namespace: str
    cluster_name: str
    dry_run: bool
    max_iterations: int
    schema_name: str
    max_event_age_seconds: int


@dataclass
class LLMConfig:
    provider: str
    model: str
    system_prompt: str


@dataclass
class NatsConfig:
    url: str
    subject: str
    consumer: str
    stream: str


@dataclass
class TaskServiceConfig:
    endpoint: str


@dataclass
class OtelConfig:
    endpoint: str
    service_name: str


@dataclass
class SchemaConfig:
    name: str
    json_schema: dict[str, Any]


@dataclass
class Config:
    kape: KapeConfig
    llm: LLMConfig
    nats: NatsConfig
    task_service: TaskServiceConfig
    otel: OtelConfig
    schema: SchemaConfig


def _dynabox_to_dict(obj: Any) -> Any:
    """Recursively convert a DynaBox (or any mapping-like) to a plain dict."""
    if hasattr(obj, "to_dict"):
        return obj.to_dict()
    elif hasattr(obj, "as_dict"):
        return obj.as_dict()
    elif hasattr(obj, "items"):
        return {k: _dynabox_to_dict(v) for k, v in obj.items()}
    elif isinstance(obj, list):
        return [_dynabox_to_dict(item) for item in obj]
    else:
        return obj


def load_config(settings_file: str | None = None) -> Config:
    resolved_file = settings_file or os.environ.get(
        "KAPE_SETTINGS_FILE", "settings.toml"
    )
    raw = Dynaconf(
        settings_file=resolved_file,
        envvar_prefix="KAPE",
        env_switcher="KAPE_ENV",
    )

    return Config(
        kape=KapeConfig(
            handler_name=raw.kape.handler_name,
            handler_namespace=raw.kape.handler_namespace,
            cluster_name=raw.kape.cluster_name,
            dry_run=raw.kape.dry_run,
            max_iterations=raw.kape.max_iterations,
            schema_name=raw.kape.schema_name,
            max_event_age_seconds=raw.kape.max_event_age_seconds,
        ),
        llm=LLMConfig(
            provider=raw.llm.provider,
            model=raw.llm.model,
            system_prompt=raw.llm.system_prompt,
        ),
        nats=NatsConfig(
            url=raw.nats.url,
            subject=raw.nats.subject,
            consumer=raw.nats.consumer,
            stream=raw.nats.stream,
        ),
        task_service=TaskServiceConfig(
            endpoint=raw.task_service.endpoint,
        ),
        otel=OtelConfig(
            endpoint=raw.otel.endpoint,
            service_name=raw.otel.service_name,
        ),
        schema=SchemaConfig(
            name=raw.schema.name,
            json_schema=_dynabox_to_dict(raw.schema.json_schema),
        ),
    )


def build_llm(llm_config: LLMConfig):
    """Build a LangChain LLM from config. API key must be in environment."""
    if llm_config.provider == "anthropic":
        from langchain_anthropic import ChatAnthropic

        return ChatAnthropic(model=llm_config.model)
    elif llm_config.provider == "openai":
        from langchain_openai import ChatOpenAI

        return ChatOpenAI(model=llm_config.model)
    else:
        raise ValueError(f"Unsupported LLM provider: {llm_config.provider!r}")
