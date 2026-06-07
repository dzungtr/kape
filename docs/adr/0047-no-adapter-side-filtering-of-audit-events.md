# Perform no adapter-side filtering of audit events

## Status

accepted

## Context

There was a question of whether the adapter should drop or filter audit events by verb or resource before publishing. The API server already applies an audit policy that filters events before invoking the webhook.

## Decision

Do no filtering in the adapter; its sole responsibility is to parse the `EventList`, translate each `Event` to a CloudEvent, and publish. Selection of which operations to audit lives in the K8s audit Policy, and selection of which events to act on lives in handler jsonpath filters.

## Consequences

The adapter stays a thin, stateless translator with a clear single responsibility, and audit scope is controlled by the API server policy that operators apply out-of-band. Adding or removing audited operations requires changing the audit Policy, not the adapter.

## Source

- [2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md](../../superpowers/specs/2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md)
