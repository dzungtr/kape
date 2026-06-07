# Scope kapetool sidecar egress per KapeTool via pod labels

## Status

accepted

## Context

NetworkPolicy applies at the pod level, not the container level, and Kubernetes has no concept of per-container network rules, so a single handler pod hosting a kapetool sidecar cannot have container-scoped egress.

## Decision

Write one NetworkPolicy per KapeTool instance, selecting handler pods by a `kape.io/tool: <tool-name>` label that the operator sets at Deployment creation time, restricting each pod to its one designated MCP server.

## Consequences

Each KapeTool gets an isolated egress path to exactly one MCP server, preventing lateral reach to other MCP servers. Engineers must duplicate the manifest per tool and keep the `kape.io/tool` and `kape.io/mcp-server` labels consistent between operator-managed and engineer-managed pods.

## Source

- [2026-05-17-phase8-issue-77-network-policy-design.md](../../superpowers/specs/2026-05-17-phase8-issue-77-network-policy-design.md)
