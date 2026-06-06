# Create KEDA ScaledObject via unstructured.Unstructured, not the keda Go module

## Status

accepted

## Context

The operator must generate KEDA ScaledObjects for autoscaling handler Deployments, which raises the question of whether to take a compile-time dependency on the `kedacore/keda/v2` Go module for typed API access. Pulling in the KEDA module would add a heavy transitive dependency and require scheme registration in the manager.

## Decision

Construct the ScaledObject as an `unstructured.Unstructured` object (apiVersion `keda.sh/v1alpha1`, kind `ScaledObject`) with no `kedacore/keda/v2` import.

## Consequences

Avoids the KEDA Go module dependency and any GVK scheme registration, since controller-runtime's `client.Client` handles unstructured objects natively. The trade-off is loss of compile-time type safety on the ScaledObject spec, and the KEDA CRD must already be installed on the cluster at runtime.

## Source

- [2026-04-19-phase6-full-operator-design.md](../../superpowers/specs/2026-04-19-phase6-full-operator-design.md)
