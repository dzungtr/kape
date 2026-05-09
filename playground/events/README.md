# Playground Event Payloads

Use the `nats` CLI to inject synthetic CloudEvents into the running playground stack.

## Prerequisites

Install the NATS CLI: https://github.com/nats-io/natscli#installation

## Inject an Alertmanager event

```bash
nats pub kape.events.alertmanager.MockApiHighErrorRate \
  --stdin < playground/events/alertmanager-example.json \
  --server nats://localhost:4222
```

## Inject a Falco event

```bash
nats pub kape.events.falco.ReadSensitiveFileUntrusted \
  --stdin < playground/events/falco-example.json \
  --server nats://localhost:4222
```

## Inject an Audit event

```bash
nats pub kape.events.audit.secrets-list \
  --stdin < playground/events/audit-example.json \
  --server nats://localhost:4222
```

## Watch all events

```bash
nats sub 'kape.events.>' --server nats://localhost:4222
```
