# Use ECDSA P-256 keys and require TLS 1.3 minimum

## Status

accepted

## Context

The mTLS implementation needed a key algorithm and TLS version policy suitable for high-frequency adapter connections while eliminating legacy weak cipher suites.

## Decision

Use ECDSA P-256 keys for all certificates and set a TLS 1.3 minimum on both client implementations (`tls.VersionTLS13` in Go, `ssl.TLSVersion.TLSv1_3` in Python).

## Consequences

Smaller keys than RSA-2048 with equivalent security yield faster handshakes and lower memory overhead. Legacy cipher suites are excluded. This relies on the NATS server and both Go and Python TLS libraries all supporting TLS 1.3.

## Source

- [2026-05-17-phase8-issue-81-mtls-nats-design.md](../../superpowers/specs/2026-05-17-phase8-issue-81-mtls-nats-design.md)
