# Reference kape/kapeproxy:local for playground and Tilt local dev

## Status

accepted

## Context

Playground compose and the Tiltfile referenced `kape/kapeproxy:0.7.0`, a tag that looks like a registry pull but is actually a locally built image, which is confusing and forces dev tooling edits on every release bump.

## Decision

`playground/docker-compose.playground.yml` and `playground/Tiltfile` build and reference `kape/kapeproxy:local`, the unversioned local-dev convention that never appears in a registry and is decoupled from any release version.

## Consequences

Local dev no longer masquerades as a release and dev tooling is insulated from release version changes. Local users must rebuild (`tilt up` / `podman compose build`) to pick up the renamed image and may clean up the old `:0.7.0` local image.

## Source

- [2026-05-17-kapeproxy-slice7-fixup.md](../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md)
