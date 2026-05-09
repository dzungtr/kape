# runtime/src/kape_runtime/skills.py
"""
Lazy-loaded skill instructions.

This module exposes :func:`make_load_skill_tool`, a factory that builds the
``load_skill`` LangChain tool. The tool reads a named skill file from
``SKILLS_DIR`` (default ``/etc/kape/skills``) and renders it through the same
Jinja2 environment + render context used for the system prompt.

INTEGRATION (Phase 7.2 — Kapeproxy MCP):
    The ReAct graph in ``graph/graph.py`` MUST construct the tool here and
    include it in the bound-tools list alongside the MCP tools, e.g.::

        load_skill_tool = make_load_skill_tool(jinja_env, render_ctx)
        tools = [*mcp_tools, load_skill_tool]
        llm_with_tools = llm.bind_tools(tools)
        tool_node = ToolNode(tools)

    where ``render_ctx`` is the same dict (``handler_name``, ``cluster_name``,
    ``namespace`` ...) used to render the system prompt. The 7.3 PR ships only
    this module and its tests; wiring is owned by 7.2.
"""
from __future__ import annotations

from pathlib import Path
from typing import Any

from jinja2 import Environment
from langchain_core.tools import tool

SKILLS_DIR = Path("/etc/kape/skills")


def _not_found(skill_name: str) -> str:
    return (
        f"Skill '{skill_name}' not found. "
        "Available skills are listed in your instructions."
    )


def make_load_skill_tool(jinja_env: Environment, context: dict[str, Any]):
    """Build the ``load_skill`` LangChain tool bound to a Jinja2 env and render context."""

    @tool
    def load_skill(skill_name: str) -> str:
        """
        Load the full instruction for a named skill.

        Call this when you determine a skill is relevant to the current
        investigation. Returns the full instruction text with all template
        variables resolved.
        """
        skills_dir = SKILLS_DIR
        if not skills_dir.exists():
            return _not_found(skill_name)
        path = skills_dir / f"{skill_name}.txt"
        if not path.exists():
            return _not_found(skill_name)
        raw = path.read_text()
        return jinja_env.from_string(raw).render(context)

    return load_skill
