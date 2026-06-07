# Use file mounts instead of env vars for KapeTool connection secrets

## Status

accepted

## Context

Environment variables in Kubernetes are readable by every process in a container, survive exec, and appear in process listings and crash dumps. The KAPE security model (spec 0007) treats the handler pod as an untrusted boundary, and env-injected Qdrant credentials are invisible to the kapeproxy output redaction (Layer 3) and contradict the audit redaction rules that suppress `$.spec.containers[*].env` from LLM visibility (Layer 7).

## Decision

Surface KapeTool connection secrets to the handler pod as read-only file mounts at `/etc/kape/secrets/<tool-name>/` rather than as env vars or `envFrom`.

## Consequences

Shrinks the LLM exfiltration surface and aligns with the existing audit redaction rationale, but requires the runtime to read credentials from files and the operator to build Volumes/VolumeMounts. A configurable `KAPE_SECRETS_DIR` (default `/etc/kape/secrets`) keeps tests and non-standard cluster layouts workable.

## Source

- [2026-05-17-phase8-issue-79-secret-management-design.md](../../superpowers/specs/2026-05-17-phase8-issue-79-secret-management-design.md)
