import pytest
from unittest.mock import AsyncMock, MagicMock
from langchain_core.messages import HumanMessage
from kape_runtime.schema_loader import make_structured_llm


@pytest.mark.asyncio
async def test_make_structured_llm_wraps_llm_with_schema():
    json_schema = {
        "type": "object",
        "required": ["decision", "confidence", "reasoning"],
        "properties": {
            "decision": {"type": "string", "enum": ["ignore", "investigate"]},
            "confidence": {"type": "number"},
            "reasoning": {"type": "string"},
        },
    }

    mock_llm = MagicMock()
    fake_structured = AsyncMock(return_value={
        "decision": "ignore",
        "confidence": 0.9,
        "reasoning": "All looks fine.",
    })
    mock_llm.with_structured_output.return_value = fake_structured

    structured = make_structured_llm(mock_llm, json_schema)

    mock_llm.with_structured_output.assert_called_once_with(json_schema)
    assert structured is fake_structured


@pytest.mark.asyncio
async def test_make_structured_llm_result_is_awaitable():
    json_schema = {
        "type": "object",
        "properties": {"answer": {"type": "string"}},
    }
    expected = {"answer": "yes"}

    mock_llm = MagicMock()
    fake_structured = MagicMock()
    fake_structured.ainvoke = AsyncMock(return_value=expected)
    mock_llm.with_structured_output.return_value = fake_structured

    structured = make_structured_llm(mock_llm, json_schema)
    result = await structured.ainvoke([HumanMessage(content="test")])
    assert result == expected
