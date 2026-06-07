# Centralize NATS mTLS connection logic in a shared Go helper

## Status

accepted

## Context

Both adapter binaries (alertmanager and audit) need identical TLS connection wiring, and duplicating it across two `main.go` files would invite drift.

## Decision

Extract the connection logic into an exported `Connect()` helper in `adapters/internal/nats/connect.go` that conditionally builds the TLS config, while leaving `publisher.go` unchanged because it operates on an already-established connection.

## Consequences

One implementation eliminates divergence between the two adapters. TLS stays a connection-level concern separate from publishing, keeping `publisher.go` untouched. Both `main.go` files reduce to reading env vars and calling the shared helper.

## Source

- [2026-05-17-phase8-issue-81-mtls-nats-design.md](../../superpowers/specs/2026-05-17-phase8-issue-81-mtls-nats-design.md)
