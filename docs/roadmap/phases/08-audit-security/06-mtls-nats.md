# Phase 8.6 — mTLS for NATS

**Status:** pending
**Phase:** 08-audit-security
**Milestone:** M4
**Specs:** 0007

## Goal

Enforce mutual TLS on all NATS connections. All publishers (adapters) and consumers (runtime) must present a valid client certificate. Non-mTLS connections are rejected.

## Work

- `examples/certs/nats-server-cert.yaml`: cert-manager `Certificate` for NATS server
- `examples/certs/nats-client-cert.yaml`: cert-manager `Certificate` for clients (one cert shared across adapters + runtime, or one per component)
- `examples/certs/issuer.yaml`: cert-manager `ClusterIssuer` (self-signed CA for local/kind; swap for production CA)
- Update NATS StatefulSet manifest (in `helm/templates/nats.yaml` or `examples/nats/`): add TLS + `verify: true` + `verify_and_map: true` to NATS config
- Update `adapters/internal/nats/publisher.go`: load client cert from file path (env var `NATS_TLS_CERT`, `NATS_TLS_KEY`, `NATS_TLS_CA`); pass to `nats.Connect()`
- Update `runtime/consumer.py`: load client cert from same env vars; pass to `nats.connect()`

## Acceptance Criteria

- Non-mTLS `nats sub` client connection rejected with TLS error
- mTLS client with valid cert connects and subscribes successfully
- Adapter publishes event → runtime consumer receives it over mTLS

## Key Files

- `examples/certs/nats-server-cert.yaml` (new)
- `examples/certs/nats-client-cert.yaml` (new)
- `examples/certs/issuer.yaml` (new)
- `adapters/internal/nats/publisher.go` (modified — TLS config)
- `runtime/consumer.py` (modified — TLS config)
