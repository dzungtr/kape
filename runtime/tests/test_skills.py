# runtime/tests/test_skills.py
from pathlib import Path

import pytest
from jinja2 import Environment

from kape_runtime import skills
from kape_runtime.skills import make_load_skill_tool


def test_load_skill_returns_rendered_content(tmp_path, monkeypatch):
    monkeypatch.setattr(skills, "SKILLS_DIR", tmp_path)
    (tmp_path / "check-orders.txt").write_text("Inspect orders for {{ cluster_name }}.")

    load_skill = make_load_skill_tool(Environment(), {"cluster_name": "kind-local"})

    result = load_skill.invoke({"skill_name": "check-orders"})

    assert result == "Inspect orders for kind-local."


def test_load_skill_missing_skill_returns_not_found(tmp_path, monkeypatch):
    monkeypatch.setattr(skills, "SKILLS_DIR", tmp_path)

    load_skill = make_load_skill_tool(Environment(), {})

    result = load_skill.invoke({"skill_name": "nonexistent"})

    assert "nonexistent" in result
    assert "not found" in result.lower()


def test_load_skill_missing_skills_dir_returns_not_found(tmp_path, monkeypatch):
    missing_dir = tmp_path / "does" / "not" / "exist"
    monkeypatch.setattr(skills, "SKILLS_DIR", missing_dir)

    load_skill = make_load_skill_tool(Environment(), {})

    result = load_skill.invoke({"skill_name": "anything"})

    assert "anything" in result
    assert "not found" in result.lower()


def test_load_skill_renders_context_variables(tmp_path, monkeypatch):
    monkeypatch.setattr(skills, "SKILLS_DIR", tmp_path)
    (tmp_path / "greet.txt").write_text(
        "Handler: {{ handler_name }} in {{ namespace }}."
    )

    load_skill = make_load_skill_tool(
        Environment(),
        {"handler_name": "alert-router", "namespace": "kape-system"},
    )

    result = load_skill.invoke({"skill_name": "greet"})

    assert result == "Handler: alert-router in kape-system."


def test_load_skill_is_a_langchain_tool():
    """The factory must return a Runnable/BaseTool with a name and description."""
    load_skill = make_load_skill_tool(Environment(), {})
    assert load_skill.name == "load_skill"
    assert load_skill.description and "skill" in load_skill.description.lower()
