# Use the K8s auditID as the CloudEvent id for dedup and correlation

## Status

accepted

## Context

Audit webhook payloads can be POSTed more than once, and operators need to correlate published events back to API server audit logs. Each K8s audit event already carries a UUID `auditID` field.

## Decision

Use the audit event's `auditID` verbatim as the CloudEvent `id`, which the shared publisher then sets as the JetStream `Nats-Msg-Id` header via `jetstream.WithMsgID`.

## Consequences

Duplicate POSTs within the JetStream dedup window become idempotent, and operators can match any CloudEvent to its API server audit log entry by `auditID`. This couples event identity to the upstream `auditID`, so any change in that field's semantics would affect dedup behavior.

## Source

- [2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md](../../superpowers/specs/2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md)
