# Deliver NetworkPolicy in both standard and Cilium CNI dialects

## Status

accepted

## Context

Clusters run different CNIs, and not all support the same policy features. KAPE needs network isolation manifests that work broadly while still offering stronger enforcement where the CNI allows it.

## Decision

Provide two manifest variants: the CNI-agnostic standard Kubernetes NetworkPolicy API (port-based LLM egress) and a strictly stronger Cilium variant (FQDN-restricted LLM egress plus cluster-entity `egressDeny`), without imposing a hard Cilium dependency.

## Consequences

Engineers choose the variant matching their CNI. The Cilium variant prevents a compromised handler pod from reaching arbitrary internet hosts on 443, but maintaining feature parity across two dialects becomes an ongoing cost for every future policy change.

## Source

- [2026-05-17-phase8-issue-77-network-policy-design.md](../../superpowers/specs/2026-05-17-phase8-issue-77-network-policy-design.md)
