# Events Broker and CloudEvents Adapter Design

## Status
**Draft**

## Problem Statement

KAPE needs a reliable event broker to decouple signal producers (Falco, AlertManager, Kubernetes audit logs) from signal consumers (handlers). Without a broker, each handler would need direct connectivity to every producer, creating a tightly coupled mesh that is difficult to scale, secure, and operate. A purpose-built event broker with wildcard subject routing, durable consumer tracking, and KEDA integration is required to make KAPE's pub/sub architecture work.

## Goals

- Provide a single, operator-managed event broker that all KAPE signal producers publish to and all KAPE handlers subscribe from.
- Define a producer-level subject hierarchy under `kape.events.*` that keeps the subject space finite and predictable for handler subscription.
- Ship three adapters (Falco, AlertManager, Kubernetes audit) that translate producer-native payloads into a uniform CloudEvents 1.0 JSON envelope.
- Authenticate all broker connections with mTLS and enforce publish-only vs subscribe+publish permissions via certificate identity.
- Enable KEDA-driven auto-scaling of handler pods based on JetStream consumer lag.

## Non-Goals

- Per-category stream isolation or independent retention policies per subject. A single `KAPE_EVENTS` stream with 24h retention covers v1 needs.
- Custom DaemonSet adapters for node-level signals. These are documented as an extension pattern but not shipped in v1.
- User-visible dashboards, SLIs, or alerting for broker health. These are delegated to standard Kubernetes observability.

## User Stories

- As a platform engineer, I want to deploy one NATS JetStream cluster into `kape-system` so that all KAPE signal producers have a single, highly available broker endpoint.
- As a platform engineer, I want to deploy a Falco adapter that translates falco-sidekick webhook payloads into CloudEvents on `kape.events.security.falco` so that handlers can subscribe to Falco signals without parsing Falco-native formats.
- As a platform engineer, I want to label PrometheusRules with `kape_subject` so that AlertManager alerts are automatically routed to the correct NATS subject.
- As a platform engineer, I want to enable Kubernetes audit log ingestion by applying the KAPE-recommended audit policy so that mutations to secrets, RBAC, and privileged pods are captured as CloudEvents.
- As a platform engineer, I want handler pods to auto-scale based on JetStream consumer lag so that burst event volume does not cause backpressure on NATS.

## Functional Requirements

- FR-1: NATS JetStream MUST be deployed as a 3-replica StatefulSet with replication factor 3 in the `kape-system` namespace.
- FR-2: Every adapter MUST authenticate to NATS using a client certificate issued by the `kape-ca` ClusterIssuer.
- FR-3: Adapter certificates MUST be restricted to publish-only on `kape.events.>`. Handler certificates MUST be permitted to both publish and subscribe on `kape.events.>`.
- FR-4: A single JetStream stream named `KAPE_EVENTS` MUST cover all subjects matching `kape.events.>` with a default retention of 24 hours.
- FR-5: Each adapter MUST emit a CloudEvents 1.0 JSON envelope containing `specversion`, `type`, `source`, `id`, `time`, `datacontenttype`, and `data`.
- FR-6: The `kape-falco-adapter` MUST receive HTTP POSTs from falco-sidekick and publish CloudEvents on subject `kape.events.security.falco`.
- FR-7: The `kape-alertmanager-adapter` MUST derive the NATS subject from the `kape_subject` label on each alert and drop alerts without this label, logging a warning.
- FR-8: The `kape-audit-adapter` MUST serve HTTPS (TLS) and publish CloudEvents on subject `kape.events.security.audit`.
- FR-9: Every adapter MUST be deployed as a single-replica Deployment in `kape-system` (not a DaemonSet).
- FR-10: JetStream durable consumers MUST follow the naming convention `kape-consumer-<handler-name>` and MUST be created and deleted by the KAPE operator, not by handler pods.

## Technical Context

The events broker sits at the centre of KAPE's pub/sub architecture. Producers (Falco, AlertManager, Kubernetes API server) emit signals through their respective adapters, which translate raw payloads into CloudEvents and publish them to NATS JetStream on subjects under `kape.events.*`. The broker stores all messages in a single `KAPE_EVENTS` stream with 24-hour retention and R=3 replication. Handlers subscribe via durable consumers that KEDA monitors for lag, driving pod auto-scaling.

The authentication model is documented in the security spec (`docs/specs/0007-security-layer`) and referenced here: mTLS via cert-manager with a two-tier certificate hierarchy (`kape-adapter-cert` for publish-only, `kape-handler-cert` for subscribe+publish). The handler runtime's sliding dedup window (referenced in ADR 0004) compensates for NATS at-least-once delivery.

The operator design (ADR 0005) specifies that the operator provisions KEDA `ScaledObject` resources and the corresponding JetStream durable consumers. The operator never needs NATS admin credentials because a single static stream covers all subjects. No stream-provisioning logic exists in the operator.

## Design Tenets

- Adapters must be stateless and single-responsibility: receive, translate, publish. No enrichment, no filtering, no business logic.
- Subjects must be producer-level, not rule-level, to keep the subject space finite and handler subscription predictable.
- Intra-producer selectivity must be handled by the handler's `trigger.filter.jsonpath` field, not by the subject name.

## Open Questions

- What is the exact Helm override mechanism for the NATS subchart — should KAPE vendor `nats/nats` as a dependency or provide a separate values file?
- How should the `kape-audit-adapter` TLS certificate be provisioned — as part of the same cert-manager flow or via a separate issuer?
- What is the exact KEDA `nats-jetstream` scaler configuration for competing-consumer mode across handler pods?

## Future Considerations

- Per-category streams with independent retention policies may become necessary if compliance requirements demand longer retention for specific event categories.
- Custom DaemonSet adapters for node-level signals without a Prometheus exporter (e.g. raw kernel events) are documented but deferred.
- Handler-to-handler chaining via event-publish action on subjects under `kape.events.gitops.*` and human-in-the-loop flows under `kape.events.approvals.*` are deferred to v2.

## References

- Full spec: `docs/specs/0006-events-broker-design/README.md`
- Security layer (mTLS details): `docs/specs/0007-security-layer`
- KapeHandler CRD (consumer/trigger model): `docs/specs/0004-kape-handler`
- Kape Operator (consumer provisioning): `docs/specs/0005-kape-operator`
