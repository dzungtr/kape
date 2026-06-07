# Use cert-manager with a self-signed ClusterIssuer CA hierarchy

## Status

accepted

## Context

mTLS requires a trusted CA to sign the NATS server certificate and all client certificates, plus automatic rotation so certificates do not silently expire in a long-running cluster.

## Decision

Use cert-manager with a self-signed bootstrap ClusterIssuer (`kape-ca`) that signs a CA Certificate, which then backs a CA ClusterIssuer (`kape-ca-issuer`) used to issue the NATS server cert and both client certs, all with `duration 8760h` and `renewBefore 720h`.

## Consequences

Certificate issuance and rotation are automated, and clients verify the NATS server cert against the same CA for full mutual trust. cert-manager becomes a deployment dependency; manifests in `examples/certs/` must be applied in order with `issuer.yaml` first. Local dev and CI run without cert-manager via the non-TLS fallback.

## Source

- [2026-05-17-phase8-issue-81-mtls-nats-design.md](../../superpowers/specs/2026-05-17-phase8-issue-81-mtls-nats-design.md)
