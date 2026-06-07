# Enforce mTLS with CN-based authorization for all NATS connections

## Status

accepted

## Context

NATS connections in kape-io were plain TCP with no transport encryption or authentication, so any workload reaching the NATS endpoint on port 4222 could inject events into the KAPE pipeline or subscribe to every security signal. This exposed two threats: unauthenticated publishers triggering handler actions and unauthenticated subscribers reading all events.

## Decision

Require mutual TLS on every NATS connection where clients must present a certificate signed by `kape-ca`, and use NATS `verify_and_map` to map the client certificate CN to a permission set, rejecting non-mTLS connections at the TLS handshake.

## Consequences

Adapters become publish-only and handlers get subscribe+publish, enforced at the broker rather than in application code. The `verify_and_map` flag is mandatory because without it all authenticated clients would share unrestricted permissions. cert-manager must be present in production, but a graceful non-TLS fallback preserves local dev and CI workflows.

## Source

- [2026-05-17-phase8-issue-81-mtls-nats-design.md](../../superpowers/specs/2026-05-17-phase8-issue-81-mtls-nats-design.md)
