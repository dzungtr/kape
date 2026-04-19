# Phase 7.3 — Load Skill Tool

**Status:** pending
**Phase:** 07-full-runtime
**Milestone:** M3
**Specs:** 0013

## Goal

Implement the `load_skill` LangChain tool that reads lazy skill instructions from `/etc/kape/skills/` and renders them via Jinja2. Register it in the graph tool registry at startup regardless of whether lazy skills exist.

## Work

- Create `runtime/skills.py`:
  ```python
  from langchain_core.tools import tool
  from pathlib import Path

  SKILLS_DIR = Path("/etc/kape/skills")

  @tool
  def load_skill(skill_name: str) -> str:
      """
      Load the full instruction for a named skill.
      Call this when you determine a skill is relevant to the current investigation.
      Returns the full instruction text with all template variables resolved.
      """
      path = SKILLS_DIR / f"{skill_name}.txt"
      if not path.exists():
          return f"Skill '{skill_name}' not found. Available skills are listed in your instructions."
      raw = path.read_text()
      return jinja_env.from_string(raw).render(context)
  ```
- `jinja_env` and `context` are the same Jinja2 env and render context used for system prompt rendering (passed in at module init or via closure)
- If `SKILLS_DIR` does not exist: `load_skill` returns not-found message, no exception
- Register `load_skill` in graph tool registry alongside `mcp_tools` in `graph.py`

## Acceptance Criteria

- Agent calls `load_skill("check-order-events")` → returns rendered instruction from `/etc/kape/skills/check-order-events.txt`
- `load_skill("nonexistent")` → returns not-found message string, no exception
- `SKILLS_DIR` missing entirely → same not-found behaviour, no exception

## Key Files

- `runtime/skills.py` (new)
- `runtime/graph/graph.py` (modified — register load_skill)
- `runtime/tests/test_skills.py` (new)
