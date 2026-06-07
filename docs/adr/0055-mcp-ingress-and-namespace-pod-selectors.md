# Restrict MCP server ingress with AND-semantics namespace and pod selectors

## Status

accepted

## Context

Handler-labelled pods could exist in namespaces other than `kape-system`; if MCP ingress matched on pod label alone, such pods could connect to MCP servers, widening the attack surface.

## Decision

In the standard MCP-server ingress policy, place `namespaceSelector` and `podSelector` in the same `from` entry to produce AND semantics, requiring both `kape.io/component: handler` and the `kape-system` namespace to match.

## Consequences

Only handler pods inside `kape-system` can reach MCP servers; cross-namespace handler-labelled pods are denied. Engineers deploying MCP servers in a different namespace must adjust both the namespace field and the `namespaceSelector` accordingly.

## Source

- [2026-05-17-phase8-issue-77-network-policy-design.md](../../superpowers/specs/2026-05-17-phase8-issue-77-network-policy-design.md)
