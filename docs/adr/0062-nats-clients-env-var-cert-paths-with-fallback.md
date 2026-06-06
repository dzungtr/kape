# Configure NATS clients via env-var cert paths with graceful non-TLS fallback

## Status

accepted

## Context

Both Go adapters and the Python runtime consumer need identical TLS wiring, but local dev and CI environments run without cert-manager and must not break `go run` or `pytest` workflows.

## Decision

Pass certificate material through three env vars (`NATS_TLS_CERT`, `NATS_TLS_KEY`, `NATS_TLS_CA`) pointing to Kubernetes-mounted Secret paths, and connect without TLS while logging a warning whenever any of the three is absent.

## Consequences

The same mechanism works in Go and Python and stays consistent with the existing adapter env-var convention. The non-TLS path is visible in logs but requires the server to also be in non-TLS mode to function, so it is local-dev-only. Certificate rotation is handled by the mounted Secret, not the application.

## Source

- [2026-05-17-phase8-issue-81-mtls-nats-design.md](../../superpowers/specs/2026-05-17-phase8-issue-81-mtls-nats-design.md)
