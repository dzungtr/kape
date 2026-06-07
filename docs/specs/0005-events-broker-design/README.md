# KAPE — Event Broker and CloudEvents Adapter Design

**Status:** Final
**Session:** 6
**Author:** Dzung Tran
**Created:** 2026-03-31

---

## Table of Contents

1. [Event Broker Decision](#1-event-broker-decision)
2. [NATS JetStream Deployment Spec](#2-nats-jetstream-deployment-spec)
3. [Authentication Model](#3-authentication-model)
4. [Stream Topology](#4-stream-topology)
5. [Subject Hierarchy](#5-subject-hierarchy)
6. [CloudEvents Adapter Design Principles](#6-cloudevents-adapter-design-principles)
7. [Adapter: kape-falco-adapter](#7-adapter-kape-falco-adapter)
8. [Adapter: kape-alertmanager-adapter](#8-adapter-kape-alertmanager-adapter)
9. [Adapter: kape-audit-adapter](#9-adapter-kape-audit-adapter)
10. [Extension Pattern: Custom DaemonSet](#10-extension-pattern-custom-daemonset)
11. [Consumer Naming Convention](#11-consumer-naming-convention)
12. [Example PrometheusRule Manifests](#12-example-prometheusrule-manifests)
13. [Decision Record](#13-decision-record)

---

## 1. Event Broker Decision

**Decision: NATS JetStream. Locked.**

Kafka and Redis Streams are no longer considered alternatives. This section records the rationale.

### Evaluation Matrix

| Criterion                   | NATS JetStream                                    | Kafka (Strimzi)                                  | Redis Streams                        |
| --------------------------- | ------------------------------------------------- | ------------------------------------------------ | ------------------------------------ |
| Operational overhead on EKS | Low — single StatefulSet, no ZooKeeper/KRaft      | High — Strimzi operator + KRaft, schema registry | Low — but not a purpose-built broker |
| KEDA scaler maturity        | ✅ Native `NatsJetStream` scaler                  | ✅ Mature `Kafka` scaler                         | ⚠️ Less production-tested            |
| Exactly-once delivery       | At-least-once (mitigated by handler dedup window) | ✅ Native via transactions                       | ❌ Not supported                     |
| Replay capability           | ✅ By sequence or time                            | ✅ Offset-based                                  | ⚠️ Manual retention management       |
| Subject wildcard model      | ✅ `kape.events.>` native                         | ⚠️ No native wildcard consumers                  | ❌ No subject hierarchy              |
| Handler-to-handler chaining | ✅ Single publish call                            | ✅ Works, adds partition management              | ⚠️ Awkward for pub/sub topology      |
| K8s-native operational feel | ✅ Lightweight, fits in kape-system               | ❌ Feels heavyweight for a cluster operator      | ✅ Lightweight but wrong semantics   |

### Rationale

The `kape.events.*` wildcard subject hierarchy is foundational to KAPE's design — it maps directly to NATS subjects and requires no additional routing configuration. KEDA's `NatsJetStream` scaler is first-class. Operational cost is dramatically lower than Kafka, which would impose a dependency (Strimzi operator, KRaft or ZooKeeper) that dwarfs KAPE itself in complexity.

The one genuine weakness — exactly-once delivery — is already mitigated by the sliding dedup window in the handler runtime. At-least-once delivery with idempotent consumers is the correct pattern for K8s operational event volumes.

---

## 2. NATS JetStream Deployment Spec

### Topology

- **StatefulSet:** 3 replicas in `kape-system`
- **Pod anti-affinity:** one pod per availability zone (hard anti-affinity rule)
- **Replication factor:** R=3 — every message stored on all three nodes, survives single-node failure
- **JetStream quorum:** 3-node Raft quorum for stream metadata

### Storage

- **Volume:** 10Gi `gp3` PVC per pod
- **Storage class:** `gp3` (EKS default) — configurable via Helm values
- **Persistence:** JetStream file-based persistence (`filestore`), not memory-only

### Helm Chart

KAPE uses the official `nats/nats` Helm chart as a subchart. KAPE's Helm chart overrides the following values:

```yaml
# values override in kape-helm
nats:
  cluster:
    enabled: true
    replicas: 3
  jetstream:
    enabled: true
    fileStorage:
      enabled: true
      size: 10Gi
      storageClassName: gp3
  affinity:
    podAntiAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        - labelSelector:
            matchLabels:
              app.kubernetes.io/name: nats
          topologyKey: topology.kubernetes.io/zone
```

### Resource Requests

```yaml
resources:
  requests:
    cpu: 100m
    memory: 256Mi
  limits:
    memory: 512Mi
```

K8s operational event volumes are low (hundreds to low thousands per day). These are conservative starting values; operators can tune via Helm values.

---

## 3. Authentication Model

**Decision: mTLS client certificates issued by cert-manager.**

### Certificate Hierarchy

```
kape-ca (ClusterIssuer, self-signed)
├── kape-adapter-cert   → issued to all adapter Deployments
│     CN: kape-adapter
│     NATS permissions: publish to kape.events.> only
│
└── kape-handler-cert   → issued to all handler pods (injected by operator)
      CN: kape-handler
      NATS permissions: subscribe to kape.events.>, publish to kape.events.>
```

### NATS TLS Configuration

NATS is configured with TLS for client connections. The NATS server's CA is the `kape-ca` ClusterIssuer. Client authentication is enforced — unauthenticated connections are rejected.

```yaml
# nats server config (tls block)
tls:
  ca_file: /etc/nats/certs/ca.crt
  cert_file: /etc/nats/certs/tls.crt
  key_file: /etc/nats/certs/tls.key
  verify: true # require client cert
```

### Certificate Mounting

Adapter Deployments mount `kape-adapter-cert` Secret at `/etc/kape/nats-certs/`.
Handler pods have `kape-handler-cert` Secret mounted by the operator at the same path.
cert-manager handles rotation automatically — no manual cert renewal.

### NATS Authorization

NATS authorization is configured via the server config `authorization` block:

```yaml
authorization:
  users:
    - nkey: <adapter-nkey>
      permissions:
        publish:
          allow: ["kape.events.>"]
        subscribe:
          deny: [">"]
    - nkey: <handler-nkey>
      permissions:
        publish:
          allow: ["kape.events.>"]
        subscribe:
          allow: ["kape.events.>"]
```

The client certificate CN is mapped to the appropriate permission set.

---

## 4. Stream Topology

**Decision: One stream `KAPE_EVENTS` covering all `kape.events.>` subjects.**

### Rationale

A single stream was chosen over per-category streams for the following reasons:

- **Operator simplicity:** the operator never needs NATS admin credentials or stream-provisioning logic. Stream topology is static and independent of `KapeHandler` CRD definitions.
- **New category = zero ops:** when an engineer deploys a `KapeHandler` for a new event category (e.g. `kape.events.iot.sensor-breach`), the subject is automatically captured by the existing stream's wildcard filter. No stream creation required.
- **Event volumes:** KAPE processes K8s operational events — hundreds to low thousands per day. Per-category stream isolation provides no meaningful throughput or isolation benefit at this scale.

Per-category streams are a v2 consideration if independent retention policies per category become a hard requirement.

### Stream Configuration

```yaml
stream:
  name: KAPE_EVENTS
  subjects:
    - kape.events.>
  retention: limits
  maxAge: 24h # 24-hour retention — configurable via Helm values
  storage: file
  replicas: 3 # R=3, matches cluster size
  discard: old # discard oldest messages when limits reached
  maxMsgs: -1 # no message count limit
  maxBytes: -1 # no byte limit (bounded by PVC size)
```

### Retention Policy

**Default: 24 hours.** K8s operational events older than 24 hours are rarely actionable. Manual replay beyond this window is an operator decision, not an automated KAPE concern.

If an operator requires longer retention (e.g. for compliance or post-mortem replay), the `maxAge` value is overridden via KAPE Helm values. This is a cluster-level operational decision, not a per-handler concern.

---

## 5. Subject Hierarchy

### Design Principle

**Subjects are producer-level, not rule-level.**

Early designs considered rule-slug subjects (e.g. `kape.events.security.falco.terminal-shell-in-container`). This was rejected because:

- Falco alone has hundreds of rules — rule-slug subjects would create an unbounded, unmanageable subject space
- A `KapeHandler` is a human-defined, finite resource — engineers define a countable number of handlers. Subjects must map to a topology that makes handler subscription predictable
- Intra-producer selectivity belongs in the handler's `trigger.filter.jsonpath` field, not in the subject name

**Rule:** one subject per producer. Handlers use `trigger.filter.jsonpath` to select the specific signals they care about within that producer's subject.

### Subject Hierarchy

```
kape.events.
├── security.
│   ├── falco         ← kape-falco-adapter (all Falco alerts)
│   ├── cilium        ← kape-alertmanager-adapter (example — engineer assigns kape_subject)
│   └── audit         ← kape-audit-adapter (all selected K8s audit events)
├── policy.
│   └── kyverno       ← kape-alertmanager-adapter (example — engineer assigns kape_subject)
├── cost.
│   └── karpenter     ← kape-alertmanager-adapter (example — engineer assigns kape_subject)
├── performance.
│   └── node          ← kape-alertmanager-adapter (example — engineer assigns kape_subject)
├── gitops.
│   └── *             ← handler-to-handler chaining via event-publish action
├── approvals.
│   └── *             ← v2 human-in-the-loop flows
└── custom.
    └── *             ← engineer-defined / DaemonSet extension pattern
```

Subjects under `security.cilium`, `policy.kyverno`, `cost.karpenter`, and `performance.node` are **documentation examples**. The actual subject name for AlertManager-sourced events is always the `kape_subject` label value set by the engineer on the PrometheusRule. KAPE enforces no schema on engineer-defined subjects beyond requiring the `kape.events.` prefix.

### Handler Subscription Examples

```yaml
# Handler subscribing to all Falco alerts, filtering to CRITICAL priority only
trigger:
  type: kape.events.security.falco
  filter:
    jsonpath: "$.data.priority"
    matches: "CRITICAL"

# Handler subscribing to all Falco alerts, filtering to a specific rule
trigger:
  type: kape.events.security.falco
  filter:
    jsonpath: "$.data.rule"
    matches: "Terminal shell in container"

# Handler subscribing to all K8s audit events, filtering to secret reads
trigger:
  type: kape.events.security.audit
  filter:
    jsonpath: "$.data.resource"
    matches: "secrets"
```

---

## 6. CloudEvents Adapter Design Principles

All adapters share the following constraints:

**Stateless:** adapters hold no local queue, no disk state, and no database connection. On NATS unavailability, the adapter retries the publish with exponential backoff (max 30s interval) and drops the message after a configurable TTL (default: 60s). Dropped messages are logged with the full CloudEvent payload for operator inspection.

**Single responsibility:** each adapter's only job is to receive a producer-specific payload and emit a valid CloudEvents 1.0 JSON envelope to NATS. No enrichment, no filtering, no business logic.

**Uniform CloudEvents envelope:**

```json
{
  "specversion": "1.0",
  "type": "<kape.events.* subject>",
  "source": "<producer>/<instance>",
  "id": "<uuid-v4>",
  "time": "<RFC3339>",
  "datacontenttype": "application/json",
  "data": { ...producer-specific payload... }
}
```

**mTLS auth:** all adapters mount the `kape-adapter-cert` Secret and present the client certificate on every NATS connection.

**Language:** Go. Single binary, minimal dependencies (`nats.go`, `cloudevents/sdk-go`). Each adapter is a separate Go binary in the `kape-adapters` repository module.

**Deployment:** each adapter is a `Deployment` with 1 replica in `kape-system`. No DaemonSet — adapters receive or watch centralised output and do not require node-local access.

---

## 7. Adapter: kape-falco-adapter

### Integration Path

```
Falco → falco-sidekick (HTTP output plugin) → kape-falco-adapter → NATS
```

Falco's native output is connected to `falco-sidekick`, which handles fan-out, output channel retry, and deduplication of output targets. KAPE is configured as a `webhook` output target in falco-sidekick's config.

falco-sidekick is **not** configured to publish directly to NATS — doing so would emit sidekick's own JSON envelope rather than a CloudEvents envelope, breaking the uniform contract. The KAPE adapter owns the CloudEvents translation.

### falco-sidekick Configuration

```yaml
# falco-sidekick config (values.yaml or ConfigMap)
config:
  webhook:
    address: http://kape-falco-adapter.kape-system.svc/events
    minimumpriority: "" # all priorities — handler filter controls selectivity
    checkcert: true
```

### Adapter Behaviour

`kape-falco-adapter` is a Go HTTP server listening on `:8080/events`. On each POST from sidekick:

1. Parse sidekick JSON payload
2. Extract: `rule`, `priority`, `output`, `output_fields`, `hostname`
3. Construct CloudEvent with `type: kape.events.security.falco`
4. Publish to NATS subject `kape.events.security.falco`

### CloudEvent Shape

```json
{
  "specversion": "1.0",
  "type": "kape.events.security.falco",
  "source": "falco/<hostname>",
  "id": "<uuid-v4>",
  "time": "<event time from sidekick payload>",
  "datacontenttype": "application/json",
  "data": {
    "rule": "Terminal shell in container",
    "priority": "WARNING",
    "output": "Warning Terminal shell in container (user=root ...)",
    "output_fields": {
      "container.id": "abc123",
      "k8s.pod.name": "api-xyz",
      "k8s.ns.name": "prod",
      "user.name": "root",
      "proc.name": "bash"
    },
    "hostname": "node-abc"
  }
}
```

**Note:** `rule` is preserved verbatim (not slugified). Handler `trigger.filter.jsonpath` matches against `$.data.rule` using the exact Falco rule name string.

### Deployment Spec

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kape-falco-adapter
  namespace: kape-system
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: adapter
          image: kape/falco-adapter:v1
          ports:
            - containerPort: 8080
          env:
            - name: NATS_URL
              value: nats://nats.kape-system.svc:4222
            - name: NATS_SUBJECT
              value: kape.events.security.falco
            - name: PUBLISH_TIMEOUT_SECONDS
              value: "60"
          volumeMounts:
            - name: nats-certs
              mountPath: /etc/kape/nats-certs
              readOnly: true
      volumes:
        - name: nats-certs
          secret:
            secretName: kape-adapter-cert
```

---

## 8. Adapter: kape-alertmanager-adapter

### Integration Path

```
Prometheus → AlertManager → kape-alertmanager-adapter → NATS
```

AlertManager is configured with a `webhook_config` receiver that POSTs alert groups to the adapter. The NATS subject is determined entirely by the `kape_subject` label on the originating PrometheusRule alert — the adapter has no subject mapping config of its own.

### AlertManager Receiver Configuration

```yaml
# alertmanager.yaml
receivers:
  - name: kape
    webhook_configs:
      - url: http://kape-alertmanager-adapter.kape-system.svc/events
        send_resolved: false # KAPE handles firing alerts only
        http_config:
          tls_config:
            ca_file: /etc/alertmanager/certs/ca.crt

route:
  routes:
    - receiver: kape
      match:
        kape_subject: /.+/ # route any alert with kape_subject label to KAPE
```

### Subject Derivation

The adapter reads `labels.kape_subject` from each alert in the payload:

- **Present:** use the value as the NATS subject directly. The engineer is responsible for ensuring the value follows the `kape.events.*` convention.
- **Absent:** log a warning with the alertname and drop the alert. No message is published to NATS.

The adapter performs no validation of the `kape_subject` value beyond checking it is a non-empty string. Subject naming convention is documented but not enforced — this keeps the adapter config-free and consistent with KAPE's principle that engineers control routing.

### Adapter Behaviour

On each POST from AlertManager (which may contain multiple alerts in one group):

1. Parse AlertManager webhook payload
2. For each alert in `alerts[]`:
   a. Read `labels.kape_subject`
   b. If absent: log warning, skip
   c. Construct CloudEvent
   d. Publish to NATS subject = `labels.kape_subject`

### CloudEvent Shape

```json
{
  "specversion": "1.0",
  "type": "kape.events.security.cilium",
  "source": "alertmanager/<labels.job>",
  "id": "<uuid-v4>",
  "time": "<alert.startsAt>",
  "datacontenttype": "application/json",
  "data": {
    "alertname": "CiliumNetworkPolicyDrop",
    "labels": {
      "severity": "warning",
      "namespace": "prod",
      "pod": "api-xyz",
      "kape_subject": "kape.events.security.cilium"
    },
    "annotations": {
      "summary": "Network policy drop rate exceeded threshold",
      "description": "..."
    },
    "startsAt": "2026-03-31T10:00:00Z",
    "generatorURL": "http://prometheus.../graph?..."
  }
}
```

The full `labels` map (including `kape_subject`) is preserved in `data.labels`. Handler prompts can reference `event.data.labels.alertname` to identify which specific alert fired.

### Deployment Spec

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kape-alertmanager-adapter
  namespace: kape-system
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: adapter
          image: kape/alertmanager-adapter:v1
          ports:
            - containerPort: 8080
          env:
            - name: NATS_URL
              value: nats://nats.kape-system.svc:4222
            - name: PUBLISH_TIMEOUT_SECONDS
              value: "60"
          volumeMounts:
            - name: nats-certs
              mountPath: /etc/kape/nats-certs
              readOnly: true
      volumes:
        - name: nats-certs
          secret:
            secretName: kape-adapter-cert
```

---

## 9. Adapter: kape-audit-adapter

### Integration Path

```
Kubernetes API Server (audit webhook backend) → kape-audit-adapter → NATS
```

The API server is configured with an audit webhook backend pointing to the adapter. The adapter receives batched audit event payloads and emits one CloudEvent per audit event.

### Audit Policy

KAPE ships a recommended audit policy manifest as part of the Helm chart. Engineers opt in by referencing it in their API server configuration. The policy is designed to capture high-signal mutations and privileged access patterns while excluding the high-volume read traffic that would overwhelm the handler.

```yaml
# kape-audit-policy.yaml (shipped in Helm chart)
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
  # Secret access — reads and writes
  - level: RequestResponse
    resources:
      - group: ""
        resources: ["secrets"]
    verbs: ["get", "create", "update", "patch", "delete"]

  # Privileged pod creation
  - level: RequestResponse
    resources:
      - group: ""
        resources: ["pods"]
    verbs: ["create"]

  # RBAC mutations — high-value security signal
  - level: RequestResponse
    resources:
      - group: "rbac.authorization.k8s.io"
        resources:
          - clusterrolebindings
          - rolebindings
          - clusterroles
          - roles
    verbs: ["create", "update", "patch", "delete"]

  # Privileged access patterns
  - level: Request
    resources:
      - group: ""
        resources: ["pods/exec", "pods/portforward", "pods/attach"]

  # ServiceAccount token creation
  - level: Metadata
    resources:
      - group: ""
        resources: ["serviceaccounts/token"]

  # Drop everything else
  - level: None
```

### API Server Webhook Configuration

```yaml
# kube-apiserver audit webhook config
apiVersion: v1
kind: Config
clusters:
  - name: kape-audit
    cluster:
      server: https://kape-audit-adapter.kape-system.svc/events
      certificate-authority: /etc/kubernetes/pki/kape-ca.crt
users:
  - name: kape-audit
    user:
      client-certificate: /etc/kubernetes/pki/kape-adapter.crt
      client-key: /etc/kubernetes/pki/kape-adapter.key
contexts:
  - name: kape-audit
    context:
      cluster: kape-audit
      user: kape-audit
current-context: kape-audit
```

### Adapter Behaviour

On each POST from the API server (batched `EventList`):

1. Parse audit `EventList`
2. For each `Event` in the list:
   a. Extract relevant fields
   b. Construct CloudEvent with `type: kape.events.security.audit`
   c. Publish to NATS subject `kape.events.security.audit`

### CloudEvent Shape

```json
{
  "specversion": "1.0",
  "type": "kape.events.security.audit",
  "source": "k8s-apiserver/<cluster-name>",
  "id": "<audit event auditID>",
  "time": "<requestReceivedTimestamp>",
  "datacontenttype": "application/json",
  "data": {
    "verb": "get",
    "resource": "secrets",
    "namespace": "prod",
    "name": "db-credentials",
    "user": {
      "username": "system:serviceaccount:prod:api",
      "groups": ["system:serviceaccounts"]
    },
    "userAgent": "kubectl/v1.35.0",
    "responseCode": 200,
    "requestObject": null,
    "responseObject": null,
    "stage": "ResponseComplete"
  }
}
```

`requestObject` and `responseObject` are included only when the audit policy level is `RequestResponse`. For `Request` level events (e.g. pod/exec), `responseObject` is null.

The audit event `auditID` is used as the CloudEvent `id` — this provides a stable, deduplicated identifier that can be correlated back to API server logs.

### Deployment Spec

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kape-audit-adapter
  namespace: kape-system
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: adapter
          image: kape/audit-adapter:v1
          ports:
            - containerPort: 8443 # HTTPS — API server requires TLS on audit webhook
          env:
            - name: NATS_URL
              value: nats://nats.kape-system.svc:4222
            - name: NATS_SUBJECT
              value: kape.events.security.audit
            - name: CLUSTER_NAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.annotations['kape.io/cluster-name']
            - name: PUBLISH_TIMEOUT_SECONDS
              value: "60"
          volumeMounts:
            - name: nats-certs
              mountPath: /etc/kape/nats-certs
              readOnly: true
            - name: tls-certs
              mountPath: /etc/kape/tls
              readOnly: true
      volumes:
        - name: nats-certs
          secret:
            secretName: kape-adapter-cert
        - name: tls-certs
          secret:
            secretName: kape-audit-adapter-tls
```

Note: the audit adapter must serve HTTPS (not HTTP) because the Kubernetes API server requires a TLS endpoint for audit webhook backends. A separate TLS certificate (also issued by cert-manager) is mounted for the server-side TLS.

---

## 10. Extension Pattern: Custom DaemonSet

The Custom DaemonSet is **not shipped as part of KAPE v1**. It is documented here as an extension pattern for engineers who need to publish node-level signals that have no Prometheus exporter.

### When to Use

Use this pattern when:

- The signal originates at the node level and is not exposed by `node_exporter`
- The signal cannot be routed through AlertManager (e.g. raw kernel events needing rich context)
- The engineer wants full control over the CloudEvent payload structure

### Pattern

Write a Go DaemonSet that:

1. Collects the node-level signal (kmsg, cgroup, inotify, etc.)
2. Constructs a valid CloudEvents 1.0 JSON envelope
3. Publishes directly to NATS using the `kape-adapter` mTLS cert
4. Uses a subject under `kape.events.custom.*` or a custom `kape.events.<category>.*` subject agreed with the platform team

```go
// Example: publish a CloudEvent directly to NATS from a DaemonSet
nc, err := nats.Connect(natsURL,
    nats.ClientCert(certFile, keyFile),
    nats.RootCAs(caFile),
)

event := cloudevents.NewEvent()
event.SetType("kape.events.custom.node.oom-kill")
event.SetSource(fmt.Sprintf("node-collector/%s", nodeName))
event.SetID(uuid.New().String())
event.SetDataContentType("application/json")
event.SetData(payload)

js, _ := nc.JetStream()
js.Publish(event.Type(), eventJSON)
```

The DaemonSet must request `kape-adapter-cert` from cert-manager — the platform team grants access to this Certificate resource via RBAC.

### Node Signals Already Covered by AlertManager

The following node-level signals are adequately covered by `node_exporter` + Prometheus + AlertManager and do **not** require a custom DaemonSet:

- Memory pressure (`node_memory_MemAvailable_bytes`)
- Disk pressure (`node_filesystem_avail_bytes`)
- OOM kill count (`node_vmstat_oom_kill`)
- CPU throttling (`container_cpu_cfs_throttled_seconds_total`)

---

## 11. Consumer Naming Convention

When the KAPE Operator generates a KEDA `ScaledObject` for a `KapeHandler`, it creates a JetStream **durable consumer** with the following naming convention:

```
kape-consumer-<handler-name>
```

Examples:

```
kape-consumer-falco-terminal-shell-handler
kape-consumer-karpenter-consolidation-watcher
kape-consumer-kyverno-policy-breach
```

### Properties

- **Durable:** the consumer name persists in NATS JetStream independent of pod lifecycle. Consumer lag is tracked correctly across scale-to-zero and back events.
- **Operator-managed:** the operator creates the durable consumer on `KapeHandler` creation and deletes it on `KapeHandler` deletion. The handler runtime connects to an existing consumer — it never creates one.
- **One consumer per handler:** each `KapeHandler` gets exactly one durable consumer. KEDA scales the number of handler pods against this consumer's lag metric. All pods in a handler Deployment share the same consumer group, achieving competing-consumer load distribution.

### KEDA ScaledObject Configuration

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: kape-scaledobject-<handler-name>
  namespace: kape-system
spec:
  scaleTargetRef:
    name: kape-handler-<handler-name>
  minReplicaCount: <spec.scaling.minReplicas>
  maxReplicaCount: <spec.scaling.maxReplicas>
  cooldownPeriod: <spec.scaling.scaleDownStabilizationSeconds>
  triggers:
    - type: nats-jetstream
      metadata:
        natsServerMonitoringEndpoint: nats.kape-system.svc:8222
        account: "$G"
        stream: KAPE_EVENTS
        consumer: kape-consumer-<handler-name>
        lagThreshold: "<spec.scaling.natsLagThreshold>"
```

---

## 12. Example PrometheusRule Manifests

KAPE ships the following example PrometheusRule manifests in the Helm chart under `examples/alert-rules/`. Engineers copy and adapt them. The `kape_subject` label is the only KAPE-specific addition to a standard alert rule.

### Cilium Network Policy Drops

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: kape-cilium-rules
  namespace: kape-system
spec:
  groups:
    - name: kape.cilium
      rules:
        - alert: CiliumNetworkPolicyDrop
          expr: |
            rate(cilium_drop_count_total{reason="Policy denied"}[5m]) > 10
          for: 2m
          labels:
            severity: warning
            kape_subject: kape.events.security.cilium
          annotations:
            summary: "High rate of Cilium network policy drops"
            description: "Drop rate {{ $value }}/s on {{ $labels.pod }}"
```

### Kyverno Policy Violation

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: kape-kyverno-rules
  namespace: kape-system
spec:
  groups:
    - name: kape.kyverno
      rules:
        - alert: KyvernoPolicyViolation
          expr: |
            increase(kyverno_policy_results_total{rule_result="fail"}[5m]) > 0
          for: 0m
          labels:
            severity: warning
            kape_subject: kape.events.policy.kyverno
          annotations:
            summary: "Kyverno policy violation detected"
            description: "Policy {{ $labels.policy_name }} failed on {{ $labels.resource_name }}"
```

### Karpenter Node Disruption

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: kape-karpenter-rules
  namespace: kape-system
spec:
  groups:
    - name: kape.karpenter
      rules:
        - alert: KarpenterNodeDisruption
          expr: |
            increase(karpenter_disruption_actions_performed_total[10m]) > 0
          for: 0m
          labels:
            severity: info
            kape_subject: kape.events.cost.karpenter
          annotations:
            summary: "Karpenter performed node disruption action"
            description: "Action {{ $labels.action }} on nodepool {{ $labels.nodepool }}"
```

---

## 13. Decision Record

| #   | Decision                                                                                                         | Rationale                                                                                                                                                             |
| --- | ---------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Event broker: NATS JetStream, locked                                                                             | Lower operational overhead than Kafka; wildcard subject model native to NATS; KEDA scaler first-class; at-least-once delivery sufficient with handler dedup window    |
| 2   | Deployment: 3-node StatefulSet, pod anti-affinity across AZs, 10Gi gp3 PVC per node, R=3                         | HA from day one; JetStream quorum; avoids StatefulSet migration later                                                                                                 |
| 3   | Auth: mTLS via cert-manager. Two certs: `kape-adapter` (publish-only), `kape-handler` (subscribe + publish)      | Production-grade; cert-manager handles rotation; no shared secrets                                                                                                    |
| 4   | Stream topology: one stream `KAPE_EVENTS`, subject filter `kape.events.>`, R=3                                   | Operator stays stateless with respect to NATS; new event categories require zero NATS ops; K8s operational event volumes do not warrant per-category stream isolation |
| 5   | Stream retention: 24h                                                                                            | Operational events rarely actionable beyond 24h; configurable via Helm values for operators with compliance requirements                                              |
| 6   | Subject granularity: producer-level, not rule-level                                                              | Finite number of handlers maps to finite number of subjects; intra-producer selectivity belongs in `trigger.filter.jsonpath`, not subject name                        |
| 7   | Falco: falco-sidekick → `kape-falco-adapter` HTTP → NATS. Subject: `kape.events.security.falco`                  | falco-sidekick handles fan-out and retry; adapter owns CloudEvents translation; single subject with full payload preserves all filtering options for handlers         |
| 8   | AlertManager: one shared `kape-alertmanager-adapter`. Subject from `kape_subject` label on PrometheusRule        | Engineer controls routing and subject assignment; adapter is config-free; consistent with AlertManager's role as the alerting fan-out layer                           |
| 9   | K8s Audit: `kape-audit-adapter`, recommended policy shipped in Helm chart, subject: `kape.events.security.audit` | Single subject with JSONPath filtering; audit policy selectivity keeps event volume manageable before it reaches NATS                                                 |
| 10  | Custom DaemonSet: not shipped in v1, documented as extension pattern for `kape.events.custom.*`                  | node_exporter + AlertManager covers all standard node signals; DaemonSet pattern reserved for signals with no Prometheus exporter                                     |
| 11  | Consumer naming: `kape-consumer-<handler-name>`, durable, operator-generated                                     | Stable across pod restarts and scale-to-zero; KEDA lag tracking correct across lifecycle events                                                                       |
