# Phase 5 — AlertManager Adapter

**Status:** done
**Milestone:** M1
**Specs:** 0006
**Modified by:** 0012 (created)

## Goal

The first real event producer. AlertManager fires a webhook → adapter normalises it to a CloudEvent → publishes to NATS. Closes the M1 loop with Phase 4.

## Reference Specs

- `0006-events-broker-design` — NATS stream topology, CloudEvent envelope schema, AlertManager adapter design

## Work

- HTTP server (Chi) at `adapters/kape-alertmanager-adapter/`
- Accept AlertManager webhook POST at `/webhook`
- Build CloudEvent: `type = kape.events.alertmanager`, `source = alertmanager`
- Publish to NATS JetStream via shared `internal/nats/` publisher
- NATS JetStream setup: create `KAPE_EVENTS` stream
- Prometheus metrics: events received, events published, publish errors

## Acceptance Criteria

- AlertManager (or `curl`) fires webhook → CloudEvent appears in NATS
- Handler pod picks up the event → Task record written with `status: completed`
- `nats sub 'kape.events.>'` shows the CloudEvent

**M1 gate:** Task record exists in DB with `status: completed` and populated `schema_output` driven by a real AlertManager alert.

## Key Files

- `adapters/kape-alertmanager-adapter/main.go`
- `adapters/internal/cloudevents/builder.go`
- `adapters/internal/nats/publisher.go`
