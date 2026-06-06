# Handler runtime is a pure message processor and never reads Kubernetes CRDs

## Status

accepted

## Context

A KapeHandler pod runs a Python + LangGraph runtime that consumes an event and runs an agent. The runtime needs skill content, tool config, file mounts, and the KapeProxy config to do its job. The obvious approach is to let the runtime read the relevant Kape* custom resources from the cluster at startup.

## Decision

The handler runtime never reads Kubernetes CRDs, never manages infrastructure, and never holds database credentials. The **operator** materialises everything the pod needs — skill content, skill file mounts, KapeProxy config, sidecars — *before* the pod starts. The runtime only processes the message it is given.

## Consequences

Keeps the runtime's RBAC surface near-zero (no cluster read access, no secrets), makes pods reproducible from their materialised inputs, and concentrates all Kubernetes-aware logic in the operator. The cost is that any change to what a handler needs at runtime must flow through the operator's materialisation step rather than being fetched on demand. Full rationale: `docs/specs/0005-kape-operator/README.md` and `docs/specs/0004-kape-handler/README.md`.
