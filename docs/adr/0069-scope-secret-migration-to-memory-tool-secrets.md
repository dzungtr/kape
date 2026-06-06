# Scope secret migration to memory-tool connection secrets only

## Status

accepted

## Context

The handler pod also handles other credentials such as the LLM provider API key, and the scope of the file-mount migration needed to be bounded.

## Decision

Move only memory-type KapeTool connection secrets (Qdrant URL and collection) to file mounts; the LLM API key from the handler's `llm.provider` credential is explicitly out of scope.

## Consequences

Keeps the change reviewable and avoids conflating distinct credential types, but leaves the LLM API key on its existing injection path as a separate future concern. The `kape-tool-` prefix and `-conn` suffix leave room for future credential types under the same naming scheme.

## Source

- [2026-05-17-phase8-issue-79-secret-management-design.md](../../superpowers/specs/2026-05-17-phase8-issue-79-secret-management-design.md)
