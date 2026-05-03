# runtime/tests/test_memory.py
from unittest.mock import MagicMock

import pytest
from langchain_core.documents import Document
from langchain_core.tools import BaseTool

from kape_runtime.memory import build_memory_tool


def test_build_memory_tool_returns_none_without_env_vars(monkeypatch):
    monkeypatch.delenv("QDRANT_URL", raising=False)
    monkeypatch.delenv("QDRANT_COLLECTION", raising=False)
    assert build_memory_tool() is None


def test_build_memory_tool_returns_none_when_only_url_set(monkeypatch):
    monkeypatch.setenv("QDRANT_URL", "http://qdrant:6333")
    monkeypatch.delenv("QDRANT_COLLECTION", raising=False)
    assert build_memory_tool() is None


def test_build_memory_tool_returns_none_when_only_collection_set(monkeypatch):
    monkeypatch.delenv("QDRANT_URL", raising=False)
    monkeypatch.setenv("QDRANT_COLLECTION", "incidents")
    assert build_memory_tool() is None


def test_build_memory_tool_returns_search_memory_tool_with_env_vars(monkeypatch):
    monkeypatch.setenv("QDRANT_URL", "http://qdrant:6333")
    monkeypatch.setenv("QDRANT_COLLECTION", "incidents")

    fake_retriever = MagicMock()
    fake_retriever.invoke.return_value = []
    fake_vstore = MagicMock()
    fake_vstore.as_retriever.return_value = fake_retriever
    vstore_factory = MagicMock(return_value=fake_vstore)
    embedding_factory = MagicMock(return_value="embedding-instance")

    tool = build_memory_tool(
        vector_store_factory=vstore_factory,
        embedding_factory=embedding_factory,
    )

    assert isinstance(tool, BaseTool)
    assert tool.name == "search_memory"
    assert "memory" in tool.description.lower()

    embedding_factory.assert_called_once_with()
    vstore_factory.assert_called_once_with(
        url="http://qdrant:6333",
        collection_name="incidents",
        embedding="embedding-instance",
    )
    fake_vstore.as_retriever.assert_called_once_with(search_kwargs={"k": 5})


def test_search_memory_returns_concatenated_doc_content(monkeypatch):
    monkeypatch.setenv("QDRANT_URL", "http://qdrant:6333")
    monkeypatch.setenv("QDRANT_COLLECTION", "incidents")

    docs = [
        Document(page_content="first memory entry"),
        Document(page_content="second memory entry"),
    ]
    fake_retriever = MagicMock()
    fake_retriever.invoke.return_value = docs
    fake_vstore = MagicMock()
    fake_vstore.as_retriever.return_value = fake_retriever

    tool = build_memory_tool(
        vector_store_factory=lambda **kw: fake_vstore,
        embedding_factory=lambda: None,
    )

    result = tool.invoke("crashloop alert")

    fake_retriever.invoke.assert_called_once_with("crashloop alert")
    assert result == "first memory entry\n\nsecond memory entry"


def test_search_memory_returns_empty_string_when_no_docs(monkeypatch):
    monkeypatch.setenv("QDRANT_URL", "http://qdrant:6333")
    monkeypatch.setenv("QDRANT_COLLECTION", "incidents")

    fake_retriever = MagicMock()
    fake_retriever.invoke.return_value = []
    fake_vstore = MagicMock()
    fake_vstore.as_retriever.return_value = fake_retriever

    tool = build_memory_tool(
        vector_store_factory=lambda **kw: fake_vstore,
        embedding_factory=lambda: None,
    )

    assert tool.invoke("nothing matches") == ""
