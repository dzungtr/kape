"""Schema loader for binding LLMs to structured outputs via JSON schemas."""
from __future__ import annotations

from typing import Any

from langchain_core.language_models import BaseChatModel
from langchain_core.runnables import Runnable


def make_structured_llm(llm: BaseChatModel, json_schema: dict[str, Any]) -> Runnable:
    """Bind llm to produce output matching json_schema via with_structured_output.

    Args:
        llm: A LangChain BaseChatModel instance.
        json_schema: A JSON schema dict describing the expected output structure.

    Returns:
        A Runnable that produces structured output matching the schema.
    """
    return llm.with_structured_output(json_schema)
