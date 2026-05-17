# runtime/tests/test_memory.py
from unittest.mock import MagicMock

import pytest
from langchain_core.documents import Document
from langchain_core.tools import BaseTool

from kape_runtime.memory import build_memory_tool


def test_build_memory_tool_returns_none_without_env_vars(monkeypatch):
    monkeypatch.delenv("KAPE_TOOL_NAME", raising=False)
    monkeypatch.delenv("KAPE_SECRETS_DIR", raising=False)
    monkeypatch.delenv("QDRANT_URL", raising=False)
    monkeypatch.delenv("QDRANT_COLLECTION", raising=False)
    assert build_memory_tool() is None


def test_build_memory_tool_returns_none_when_only_url_set(monkeypatch):
    monkeypatch.delenv("KAPE_TOOL_NAME", raising=False)
    monkeypatch.delenv("KAPE_SECRETS_DIR", raising=False)
    monkeypatch.setenv("QDRANT_URL", "http://qdrant:6333")
    monkeypatch.delenv("QDRANT_COLLECTION", raising=False)
    assert build_memory_tool() is None


def test_build_memory_tool_returns_none_when_only_collection_set(monkeypatch):
    monkeypatch.delenv("KAPE_TOOL_NAME", raising=False)
    monkeypatch.delenv("KAPE_SECRETS_DIR", raising=False)
    monkeypatch.delenv("QDRANT_URL", raising=False)
    monkeypatch.setenv("QDRANT_COLLECTION", "incidents")
    assert build_memory_tool() is None


def test_build_memory_tool_returns_search_memory_tool_with_env_vars(monkeypatch):
    monkeypatch.delenv("KAPE_TOOL_NAME", raising=False)
    monkeypatch.delenv("KAPE_SECRETS_DIR", raising=False)
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
    monkeypatch.delenv("KAPE_TOOL_NAME", raising=False)
    monkeypatch.delenv("KAPE_SECRETS_DIR", raising=False)
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
    monkeypatch.delenv("KAPE_TOOL_NAME", raising=False)
    monkeypatch.delenv("KAPE_SECRETS_DIR", raising=False)
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


# --- Part C: file-mount secret tests ---


def _make_vstore_factory():
    fake_retriever = MagicMock()
    fake_retriever.invoke.return_value = []
    fake_vstore = MagicMock()
    fake_vstore.as_retriever.return_value = fake_retriever
    vstore_factory = MagicMock(return_value=fake_vstore)
    return vstore_factory


def test_reads_from_file(monkeypatch, tmp_path):
    tool_dir = tmp_path / "my-tool"
    tool_dir.mkdir()
    (tool_dir / "qdrant_url").write_text("http://from-file:6333\n")
    (tool_dir / "qdrant_collection").write_text("file-collection\n")

    monkeypatch.setenv("KAPE_SECRETS_DIR", str(tmp_path))
    monkeypatch.setenv("KAPE_TOOL_NAME", "my-tool")
    monkeypatch.delenv("QDRANT_URL", raising=False)
    monkeypatch.delenv("QDRANT_COLLECTION", raising=False)

    vstore_factory = _make_vstore_factory()
    embedding_factory = MagicMock(return_value="embedding-instance")

    tool = build_memory_tool(
        vector_store_factory=vstore_factory,
        embedding_factory=embedding_factory,
    )

    assert tool is not None
    vstore_factory.assert_called_once_with(
        url="http://from-file:6333",
        collection_name="file-collection",
        embedding="embedding-instance",
    )


def test_env_var_fallback_when_tool_name_unset(monkeypatch):
    monkeypatch.delenv("KAPE_TOOL_NAME", raising=False)
    monkeypatch.delenv("KAPE_SECRETS_DIR", raising=False)
    monkeypatch.setenv("QDRANT_URL", "http://env-qdrant:6333")
    monkeypatch.setenv("QDRANT_COLLECTION", "env-collection")

    vstore_factory = _make_vstore_factory()
    embedding_factory = MagicMock(return_value="embedding-instance")

    tool = build_memory_tool(
        vector_store_factory=vstore_factory,
        embedding_factory=embedding_factory,
    )

    assert tool is not None
    vstore_factory.assert_called_once_with(
        url="http://env-qdrant:6333",
        collection_name="env-collection",
        embedding="embedding-instance",
    )


def test_returns_none_when_no_values(monkeypatch, tmp_path):
    """KAPE_TOOL_NAME set, secrets dir exists but is empty, no env-var fallback."""
    monkeypatch.setenv("KAPE_TOOL_NAME", "my-tool")
    monkeypatch.setenv("KAPE_SECRETS_DIR", str(tmp_path))
    monkeypatch.delenv("QDRANT_URL", raising=False)
    monkeypatch.delenv("QDRANT_COLLECTION", raising=False)

    assert build_memory_tool() is None


def test_env_fallback_when_secrets_dir_missing(monkeypatch):
    """Secret volume not yet mounted — fall back to env vars without crashing."""
    monkeypatch.setenv("KAPE_TOOL_NAME", "my-tool")
    monkeypatch.setenv("KAPE_SECRETS_DIR", "/nonexistent/path/that/does/not/exist")
    monkeypatch.setenv("QDRANT_URL", "http://from-env:6333")
    monkeypatch.setenv("QDRANT_COLLECTION", "env-coll")

    vstore_factory = _make_vstore_factory()
    embedding_factory = MagicMock(return_value="embedding-instance")

    tool = build_memory_tool(
        vector_store_factory=vstore_factory,
        embedding_factory=embedding_factory,
    )

    assert tool is not None
    vstore_factory.assert_called_once_with(
        url="http://from-env:6333",
        collection_name="env-coll",
        embedding="embedding-instance",
    )


def test_file_takes_precedence_over_env(monkeypatch, tmp_path):
    tool_dir = tmp_path / "my-tool"
    tool_dir.mkdir()
    (tool_dir / "qdrant_url").write_text("file-url\n")
    (tool_dir / "qdrant_collection").write_text("file-coll\n")

    monkeypatch.setenv("KAPE_SECRETS_DIR", str(tmp_path))
    monkeypatch.setenv("KAPE_TOOL_NAME", "my-tool")
    monkeypatch.setenv("QDRANT_URL", "env-url")
    monkeypatch.setenv("QDRANT_COLLECTION", "env-coll")

    vstore_factory = _make_vstore_factory()
    embedding_factory = MagicMock(return_value="embedding-instance")

    tool = build_memory_tool(
        vector_store_factory=vstore_factory,
        embedding_factory=embedding_factory,
    )

    assert tool is not None
    vstore_factory.assert_called_once_with(
        url="file-url",
        collection_name="file-coll",
        embedding="embedding-instance",
    )


def test_env_fallback_when_only_one_file_missing(monkeypatch, tmp_path):
    tool_dir = tmp_path / "my-tool"
    tool_dir.mkdir()
    (tool_dir / "qdrant_url").write_text("http://from-file:6333\n")
    # qdrant_collection file deliberately absent

    monkeypatch.setenv("KAPE_SECRETS_DIR", str(tmp_path))
    monkeypatch.setenv("KAPE_TOOL_NAME", "my-tool")
    monkeypatch.delenv("QDRANT_URL", raising=False)
    monkeypatch.setenv("QDRANT_COLLECTION", "env-collection")

    vstore_factory = _make_vstore_factory()
    embedding_factory = MagicMock(return_value="embedding-instance")

    tool = build_memory_tool(
        vector_store_factory=vstore_factory,
        embedding_factory=embedding_factory,
    )

    assert tool is not None
    vstore_factory.assert_called_once_with(
        url="http://from-file:6333",
        collection_name="env-collection",
        embedding="embedding-instance",
    )
