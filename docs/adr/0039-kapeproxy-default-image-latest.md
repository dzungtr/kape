# Default the KapeProxy operator image version to latest, not a release pin

## Status

accepted

## Context

PR #57 hardcoded the release pin `0.7.0` (and previously `stub`) as the operator's in-code default image version, coupling the operator binary to a specific kapeproxy release that must be unwound on every image bump. The IMPLEMENTATION-SPEC actually mandates `latest` as the operator default.

## Decision

`KapeproxyImageVersion` defaults to `latest` in `operator/domain/config/config.go` for both the inline `KapeproxyImageRef()` default and the `WithDefaults()` assignment, while release pins live exclusively in `helm/values.yaml`.

## Consequences

The operator works on a fresh cluster without chart values and is decoupled from release cadence, but operators needing a pinned kapeproxy release must set `kapeproxy.version` in kape-config or deploy via the Helm chart. This is an operator-facing behavior change (`0.7.0` to `latest`) that must be called out in upgrade notes.

## Source

- [2026-05-17-kapeproxy-slice7-fixup.md](../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md)
