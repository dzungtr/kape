# Defer Qdrant collection creation to the runtime, not the operator

## Status

accepted

## Context

Memory-type KapeTools back onto a Qdrant vector store, raising the question of which component is responsible for provisioning the Qdrant collection. The operator could call the Qdrant HTTP API to create collections, or this could be left to the consuming runtime.

## Decision

The operator provisions only the Qdrant StatefulSet plus a headless Service and makes no Qdrant HTTP API calls; the handler runtime (LangChain `QdrantVectorStore`) creates the collection lazily on first use.

## Consequences

Keeps the operator's reconcile loop free of data-plane HTTP calls to Qdrant and simplifies the memory reconciler to readiness checks plus endpoint publishing. Collection lifecycle becomes a runtime concern (Phase 7) and is therefore out of scope for the operator.

## Source

- [2026-04-19-phase6-full-operator-design.md](../../superpowers/specs/2026-04-19-phase6-full-operator-design.md)
