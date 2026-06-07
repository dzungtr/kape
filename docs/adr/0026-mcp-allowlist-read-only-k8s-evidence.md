# Scope handler agent evidence-gathering to read-only k8s ops via MCP allowlists

## Status

accepted

## Context

A KapeHandler agent needs to fetch live cluster state (pod logs) as investigation evidence during its reasoning loop, but giving an autonomous agent unrestricted cluster access is a security risk. The SRE example must let the agent read mock-api pod logs without granting mutate capability.

## Decision

The log-reader KapeTool connects to the shared k8s-mcp server but restricts the agent to an explicit allowlist of read-only operations (`k8s-mcp-read__get_pod_logs`, `k8s-mcp-read__list_pods`).

## Consequences

This establishes the pattern that agent investigation tools are scoped by per-tool MCP allowlists rather than broad RBAC, keeping read and mutate surfaces separate. RBAC for the MCP itself follows existing KapeTool RBAC patterns, so the allowlist is an additional in-handler guardrail rather than a replacement for cluster permissions.

## Source

- [2026-04-19-sre-alertmanager-example-design.md](../../superpowers/specs/2026-04-19-sre-alertmanager-example-design.md)
