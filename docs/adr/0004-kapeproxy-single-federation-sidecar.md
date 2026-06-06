# KapeProxy: one MCP federation sidecar per pod instead of one sidecar per KapeTool

## Status

accepted

## Context

The original model injected one `kapetool` sidecar per referenced KapeTool. That worked when tool refs came only from `spec.tools[]` on the handler. KapeSkill changed this: skills declare their own tool dependencies, so three skills each referencing two tools could mean six-plus sidecars per pod — unbounded proliferation that hits Kubernetes container limits.

## Decision

Inject exactly **one KapeProxy sidecar per handler pod**. It acts as an MCP federation layer: connects to all upstream MCP servers, filters by per-tool allowlists, namespaces tool names, and exposes a single unified MCP endpoint to the handler runtime. The operator unions tool refs from both the handler and its skills into one KapeProxy config.

## Consequences

Pod topology stays flat regardless of how many tools skills pull in, and the runtime sees one MCP endpoint instead of N. The proxy becomes a single point of failure and a new component to operate, and tool-name namespacing/allowlist filtering moves into it. This decision is what makes KapeSkill tractable. Full rationale: `docs/specs/0013-kape-skill-crd/README.md` and `docs/specs/0004-kape-handler/README.md`.
