# Enforce the domain package dependency boundary

## Status

accepted

## Context

The domain package must encapsulate pure decision logic and remain testable without a Kubernetes API server. Allowing it to reach for controller-runtime, `client.Client`, or infra adapters would dissolve the functional-core boundary the package exists to enforce.

## Decision

Permit `domain/` to import only v1alpha1 CRD types, stdlib, and `k8s.io/apimachinery/pkg/api/meta`; forbid imports of controller-runtime, `client.Client`, `infra/ports`, and `infra/k8s` adapters.

## Consequences

Guarantees the domain layer stays pure and envtest-free, enforceable as an acceptance criterion. Any dependency a domain method needs (e.g. a future registry) must be passed as a method argument rather than stored on the wrapper, keeping the boundary intact as signatures extend.

## Source

- [2026-05-10-handler-reconciler-domain-decomposition-design.md](../../superpowers/specs/2026-05-10-handler-reconciler-domain-decomposition-design.md)
