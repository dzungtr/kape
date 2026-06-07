# Defer NATS mTLS to a later issue; use plain nats.Connect for the audit adapter now

## Status

accepted

## Context

Two distinct certificates are in play: a server TLS cert for the HTTPS webhook endpoint and a NATS client mTLS cert (`kape-adapter-cert`) for authenticating to NATS. Rolling out NATS mTLS spans all adapters and is tracked separately under issue #81.

## Decision

Connect to NATS with a plain non-mTLS `nats.Connect` call (matching the alertmanager adapter) and omit the `nats-certs` volume mount from this issue's Deployment, deferring client mTLS to issue #81.

## Consequences

This issue stays scoped to the HTTPS webhook listener and avoids coupling to the cross-adapter mTLS rollout. It leaves NATS traffic unauthenticated until #81 lands, and the Deployment manifest will need the `nats-certs` mount added later.

## Source

- [2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md](../../superpowers/specs/2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md)
