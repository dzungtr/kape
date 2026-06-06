# Give the audit adapter its own CloudEvent builder package

## Status

accepted

## Context

The shared `cloudevents.Build` function is AlertManager-specific, but the audit adapter needs to translate K8s audit Events into CloudEvents with an audit-specific data schema (verb, resource, user, response code, request/response objects).

## Decision

Add `AuditInput`/`BuildAudit` and the audit `EventData` type to a dedicated `adapters/internal/audit` package rather than extending the shared `cloudevents` package, leaving the existing AlertManager builder unchanged.

## Consequences

Each adapter owns its own event-translation logic and data schema, keeping adapter packages independent and avoiding a shared builder that must know every producer's schema. The `Publisher` interface is duplicated per package by design to preserve that independence.

## Source

- [2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md](../../superpowers/specs/2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md)
