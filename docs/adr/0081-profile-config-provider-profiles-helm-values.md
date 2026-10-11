# Profile reconciler config surface: provider profiles + Helm values

## Status

accepted

## Context

Decision #157 (profile reconciler syncing registry images + OpenShell templates) planned to "watch #1886 (declarative gateway.toml) to shrink the shim", expecting OpenShell to ship a declarative gateway config file. Live upstream verification (2026-10-11, `docs/upstream-pins.md`, issue #163 / PR #193): #1886 closed **not_planned** (2026-09-21). The shipped direction is provider profiles — authoritative since #2962 (OpenShell v0.1.0) — plus Helm values, with HTTP profile import (#3706) and on-demand sandbox config files (#3832, v0.1.3).

## Decision

The profile reconciler's shim (#157) targets OpenShell provider profiles plus Helm values as the configuration surface. No declarative gateway.toml will exist; the shim is designed against profiles + Helm values.

## Consequences

- The "shrink the shim" hope dies with #1886: the shim's size is bounded by the profile/Helm surface, and it must track profile schema changes across OpenShell upgrades.
- Pin implication: provider-profile-authoritative behavior arrives with OpenShell ≥ v0.1.0 — kape's current v0.0.116 pin does not contain it (see `docs/upstream-pins.md`).
