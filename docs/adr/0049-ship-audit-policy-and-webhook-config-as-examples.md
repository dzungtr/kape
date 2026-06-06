# Ship the audit Policy and webhook kubeconfig as examples, not Helm resources

## Status

accepted

## Context

The recommended K8s audit Policy and the API server webhook kubeconfig must be applied to the API server's own configuration (via `--audit-policy-file` and `--audit-webhook-config-file`), which is node/control-plane level rather than in-cluster. The question was whether Helm should manage these.

## Decision

Ship `kape-audit-policy.yaml` and `kape-audit-webhook-config.yaml` under `examples/audit-policy/` as documented examples, not as Helm-templated cluster resources.

## Consequences

Operators apply these to the API server out-of-band, acknowledging that audit configuration cannot be installed via Helm into a running cluster. The webhook kubeconfig must reference the exported `kape-ca` CA cert placed on the API server node, making this a manual operator step.

## Source

- [2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md](../../superpowers/specs/2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md)
