# Warm pooling is agent-sandbox-native — driven via extension CRDs, not OpenShell

## Status

accepted

## Context

Decision #158 locked warm pools as "template-backed, hot profiles only, cold path default" and gated them on "requires OpenShell ≥ PR #2460", assuming OpenShell would ship warm-pool support for its k8s driver. Live upstream verification (2026-10-11, `docs/upstream-pins.md`, issue #163 / PR #193) shows the opposite: OpenShell PR #2460 closed **unmerged**, the earlier PR #1892 closed **unmerged**, and umbrella issue #1879 was closed **not_planned**. Warm pooling lives in agent-sandbox itself — extension CRDs `SandboxWarmPool`/`SandboxClaim` (`extensions.agents.x-k8s.io`) shipping since agent-sandbox v0.1.0 (2025-11-07) — and OpenShell merely consumes warm candidates via its agent-sandbox install.

## Decision

The platform drives `SandboxWarmPool` and `SandboxClaim` (agent-sandbox extension CRDs) directly; OpenShell is not on the warm-pool path. We do not wait for OpenShell-side pooling (dead upstream) and do not build platform-side pooling logic (reinvents what agent-sandbox ships).

## Consequences

- #158's gates stand (ready_within < 5s, trusted templates) with the dependency line corrected: no OpenShell version requirement exists.
- agent-sandbox ≥ v1.0.6 supplies the M2 hooks: `SandboxClaim` labels/selectors and `--sandbox-claim-warm-candidate-grace-period` (cold-create fallback, default 2s).
- Sizing/economics stay open in #166.
