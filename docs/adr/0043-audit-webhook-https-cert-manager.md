# Serve the audit webhook over HTTPS on :8443 with a cert-manager certificate

## Status

accepted

## Context

The Kubernetes API server rejects plain-HTTP audit webhook backends and enforces TLS on audit targets, but KAPE's existing adapters (alertmanager, falco) serve plain HTTP on `:8080`. The audit adapter is the first adapter that must terminate TLS itself.

## Decision

Serve the webhook over HTTPS on `:8443` using a server certificate named `kape-audit-adapter-tls`, issued by the `kape-ca` ClusterIssuer via a cert-manager Certificate and mounted read-only at `/etc/kape/tls/` as `tls.crt`/`tls.key`.

## Consequences

The adapter can be registered as a valid K8s audit webhook backend, and certificate lifecycle (issuance, renewal) is delegated to cert-manager. It establishes the first TLS-terminating adapter pattern and adds a cert-manager + kape-ca ClusterIssuer dependency to the deployment.

## Source

- [2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md](../../superpowers/specs/2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md)
