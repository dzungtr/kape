# Source cluster name from CLUSTER_NAME env via a Deployment annotation fieldRef

## Status

accepted

## Context

The CloudEvent `source` field must identify the originating cluster as `k8s-apiserver/<cluster-name>`, but the adapter has no intrinsic knowledge of which cluster it runs in. A consistent injection mechanism is needed across deployments.

## Decision

Inject the cluster name via the `CLUSTER_NAME` environment variable, populated from the `kape.io/cluster-name` Deployment annotation through a `fieldRef`, defaulting to `unknown` when empty.

## Consequences

The cluster identity is configurable per deployment without code changes and travels with the manifest annotation. Events from a misconfigured deployment degrade gracefully to source `k8s-apiserver/unknown` rather than failing.

## Source

- [2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md](../../superpowers/specs/2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md)
