# Pass the system prompt into TOMLRenderer.Render as a parameter

## Status

accepted

## Context

`ports.TOMLRenderer.Render` previously re-derived the system prompt internally by calling `AssembleSystemPrompt`, coupling the infra renderer to prompt-assembly logic. After the refactor the prompt becomes a pure domain output produced by `Handler.SystemPrompt`.

## Decision

Change the `TOMLRenderer.Render` signature to accept `systemPrompt string` (and `lazySkills`) as parameters, removing the renderer's internal prompt derivation; the reconciler passes the domain-derived prompt.

## Consequences

Prompt assembly lives solely in the domain, keeping the renderer a pure formatter. This is the only port-shape change in the refactor; `reconcile/system_prompt.go` is deleted and its tests move to the domain. The change is internal to the operator with no external API surface.

## Source

- [2026-05-10-handler-reconciler-domain-decomposition-design.md](../../superpowers/specs/2026-05-10-handler-reconciler-domain-decomposition-design.md)
