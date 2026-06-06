# Inject events by publishing CloudEvents directly to NATS subjects

## Status

accepted

## Context

The playground needs a way to trigger handler execution and to verify adapters without a live alert source, while keeping each of the three use cases independently testable.

## Decision

Use the `nats` CLI to publish events directly to NATS subjects (with sample payloads in `playground/events/*.json`), and have adapters fire CloudEvents to subjects like `kape.events.alertmanager.<alert-name>` via `make fire-adapter ADAPTER=<name> --playground`.

## Consequences

Event injection and adapter verification stay decoupled from the runtime, so UC2 and UC3 can be exercised independently. Confirms NATS subjects as the integration boundary between adapters and the runtime, with CloudEvents as the wire format.

## Source

- [2026-05-03-local-dev-playground-design.md](../../superpowers/specs/2026-05-03-local-dev-playground-design.md)
