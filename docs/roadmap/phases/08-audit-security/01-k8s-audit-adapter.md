# Phase 8.1 — K8s Audit Adapter

**Status:** pending
**Phase:** 08-audit-security
**Milestone:** M4
**Specs:** 0006

## Goal

New Go adapter that accepts K8s API server audit webhook events and publishes them as CloudEvents to NATS JetStream on subject `kape.events.audit.<verb>.<resource>`.

## Work

- New service at `adapters/kape-audit-adapter/`
- Chi HTTP server; POST `/webhook` accepts K8s `EventList` audit payload
- Parse each audit event: extract `verb`, `objectRef.resource`, `objectRef.name`, `objectRef.namespace`, `requestObject`
- Audit policy scope: verbs `create`, `update`, `delete` on `secrets`, `rolebindings`, `clusterrolebindings`, `pods`
- Build CloudEvent: `type = kape.events.audit`, `source = k8s-apiserver`, subject = `kape.events.audit.<verb>.<resource>`
- Publish via shared `adapters/internal/nats/publisher.go`
- Prometheus metrics: `kape_audit_events_received_total`, `kape_audit_events_published_total`, `kape_audit_publish_errors_total`

## Acceptance Criteria

- POST a synthetic K8s audit event for a Secret creation → CloudEvent appears in NATS on `kape.events.audit.create.secrets`
- Handler pod picks up event → Task record written with `status: completed`
- Events for non-watched verbs/resources are silently dropped (no NATS publish)

## Key Files

- `adapters/kape-audit-adapter/main.go` (new)
- `adapters/kape-audit-adapter/handler.go` (new)
- `adapters/internal/cloudevents/builder.go` (modified — audit CloudEvent type)
- `adapters/internal/nats/publisher.go` (shared, unchanged)
