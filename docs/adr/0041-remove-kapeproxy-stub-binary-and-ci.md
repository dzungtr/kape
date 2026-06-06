# Remove the slice-5 kapeproxy stub binary and its CI pipeline

## Status

accepted

## Context

The slice-5 stub binary was time-bounded by D2+R1 to be removed in slice 7, but while PR #57 removed the binary it left `.github/workflows/kapeproxy-stub.yml` in the tree, still able to publish `kape/kapeproxy:stub` images on `workflow_dispatch`.

## Decision

Delete `.github/workflows/kapeproxy-stub.yml` as part of the fixup, completing slice-7 Task 12 so no future PR can push a `kape/kapeproxy:stub` tag to any registry.

## Consequences

The stub artifact is fully retired, closing the path for it to drift back into use. No runtime behavior changes since the stub binary itself was already gone.

## Source

- [2026-05-17-kapeproxy-slice7-fixup.md](../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md)
