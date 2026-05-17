# runtime/src/kape_runtime/memory.py
from __future__ import annotations

import os
import pathlib
from typing import Any, Callable

from langchain_core.tools import BaseTool, Tool


def _default_vector_store_factory(
    *, url: str, collection_name: str, embedding: Any
) -> Any:
    from langchain_qdrant import QdrantVectorStore

    return QdrantVectorStore.from_existing_collection(
        url=url,
        collection_name=collection_name,
        embedding=embedding,
    )


def _default_embedding_factory() -> Any:
    from langchain_openai import OpenAIEmbeddings

    return OpenAIEmbeddings()


def build_memory_tool(
    *,
    vector_store_factory: Callable[..., Any] = _default_vector_store_factory,
    embedding_factory: Callable[[], Any] = _default_embedding_factory,
) -> BaseTool | None:
    """Build a `search_memory` tool backed by a Qdrant vector store.

    Read order for each of url and collection:
      1. File at $KAPE_SECRETS_DIR/<tool-name>/{qdrant_url,qdrant_collection}
         (file mount from a per-tool Secret)
      2. Environment variable QDRANT_URL or QDRANT_COLLECTION (local dev fallback)

    Returns None if either value cannot be resolved — handlers without a memory
    backend simply omit the tool from the graph.
    """
    secrets_dir = os.environ.get("KAPE_SECRETS_DIR", "/etc/kape/secrets")
    tool_name   = os.environ.get("KAPE_TOOL_NAME")

    def _read_secret(filename: str, env_fallback: str) -> str | None:
        if tool_name:
            path = os.path.join(secrets_dir, tool_name, filename)
            if os.path.exists(path):
                return pathlib.Path(path).read_text().strip()
        return os.environ.get(env_fallback)

    url        = _read_secret("qdrant_url", "QDRANT_URL")
    collection = _read_secret("qdrant_collection", "QDRANT_COLLECTION")

    if not url or not collection:
        return None

    embedding = embedding_factory()
    vstore = vector_store_factory(
        url=url,
        collection_name=collection,
        embedding=embedding,
    )
    retriever = vstore.as_retriever(search_kwargs={"k": 5})

    def _search(query: str) -> str:
        docs = retriever.invoke(query)
        return "\n\n".join(d.page_content for d in docs)

    return Tool(
        name="search_memory",
        description=(
            "Search persistent memory for prior incidents and notes related to the "
            "current event. Pass a search query string; returns matching documents "
            "concatenated as plain text."
        ),
        func=_search,
    )
