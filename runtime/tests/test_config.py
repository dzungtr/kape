# runtime/tests/test_config.py
import os
import pytest
from pathlib import Path
from kape_runtime.config import load_config, KapeConfig, LLMConfig, NatsConfig


def test_load_config_returns_typed_config(tmp_path):
    settings_file = tmp_path / "settings.toml"
    settings_file.write_text(
        """
[kape]
handler_name = "test-handler"
handler_namespace = "default"
cluster_name = "kind-local"
dry_run = false
max_iterations = 10
schema_name = "test-schema"
max_event_age_seconds = 300

[llm]
provider = "anthropic"
model = "claude-haiku-4-5-20251001"
system_prompt = "You are a test agent."

[nats]
url = "nats://localhost:4222"
subject = "kape.events.alertmanager"
consumer = "kape-consumer-test"
stream = "KAPE_EVENTS"

[task_service]
endpoint = "http://localhost:8080"

[otel]
endpoint = "http://localhost:4318"
service_name = "kape-handler"

[schema]
name = "test-schema"

[schema.json_schema]
type = "object"
required = ["decision", "confidence", "reasoning"]

[schema.json_schema.properties.decision]
type = "string"

[schema.json_schema.properties.confidence]
type = "number"

[schema.json_schema.properties.reasoning]
type = "string"
"""
    )
    config = load_config(str(settings_file))
    assert config.kape.handler_name == "test-handler"
    assert config.kape.cluster_name == "kind-local"
    assert config.kape.dry_run is False
    assert config.kape.max_event_age_seconds == 300
    assert config.llm.provider == "anthropic"
    assert config.llm.model == "claude-haiku-4-5-20251001"
    assert config.nats.subject == "kape.events.alertmanager"
    assert config.nats.stream == "KAPE_EVENTS"
    assert config.task_service.endpoint == "http://localhost:8080"
    assert config.otel.endpoint == "http://localhost:4318"
    assert config.schema.name == "test-schema"
    assert config.schema.json_schema["type"] == "object"
    assert "decision" in config.schema.json_schema["required"]


def test_env_var_overrides_settings(tmp_path, monkeypatch):
    settings_file = tmp_path / "settings.toml"
    settings_file.write_text(
        """
[kape]
handler_name = "original"
handler_namespace = "default"
cluster_name = "kind-local"
dry_run = false
max_iterations = 10
schema_name = "test-schema"
max_event_age_seconds = 300

[llm]
provider = "anthropic"
model = "claude-haiku-4-5-20251001"
system_prompt = "prompt"

[nats]
url = "nats://localhost:4222"
subject = "kape.events.alertmanager"
consumer = "kape-consumer-test"
stream = "KAPE_EVENTS"

[task_service]
endpoint = "http://localhost:8080"

[otel]
endpoint = "http://localhost:4318"
service_name = "kape-handler"

[schema]
name = "test-schema"

[schema.json_schema]
type = "object"
"""
    )
    monkeypatch.setenv("KAPE_KAPE__HANDLER_NAME", "overridden")
    config = load_config(str(settings_file))
    assert config.kape.handler_name == "overridden"
