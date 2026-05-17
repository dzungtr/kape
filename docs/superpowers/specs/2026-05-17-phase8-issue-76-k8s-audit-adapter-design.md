# Phase 8.1 — K8s Audit Adapter

**Status:** Draft
**Date:** 2026-05-17
**GitHub Issue:** #76
**Phase:** 08-audit-security
**Milestone:** M4
**Reference Specs:** 0006

---

## Goal

Implement `kape-audit-adapter`: a Go HTTPS service that receives batched Kubernetes API server audit webhook payloads, translates each audit event into a CloudEvents 1.0 envelope, and publishes to NATS JetStream on the single subject `kape.events.security.audit`.

---

## Background

The Kubernetes API server can be configured with an audit webhook backend. When enabled, it POSTs batched `EventList` objects (schema: `audit.k8s.io/v1`) to the backend URL after each audited API operation. The backend must serve HTTPS because the API server enforces TLS on audit webhook targets.

KAPE's existing adapters (alertmanager, falco) serve plain HTTP on `:8080`. The audit adapter is the first adapter that must serve HTTPS — this requires a server TLS certificate issued by cert-manager and mounted into the pod.

### Subject correction

The iteration file at `docs/roadmap/phases/08-audit-security/01-k8s-audit-adapter.md` states the subject as `kape.events.audit.<verb>.<resource>`. This is incorrect and must not be implemented. Spec 0006 (section 5, "Subject Hierarchy") locks the design principle: **one subject per producer**. Intra-producer selectivity is the responsibility of handler `trigger.filter.jsonpath` filters, not subject name decomposition. The correct subject is `kape.events.security.audit` — a single subject for all audit events regardless of verb or resource.

---

## Architecture

```
Kubernetes API Server
        │  HTTPS POST /webhook
        ▼
kape-audit-adapter (:8443, TLS)
        │  server cert: kape-audit-adapter-tls (cert-manager)
        │  mounted at /etc/kape/tls/
        │
        │  parse audit.k8s.io/v1 EventList
        │  build CloudEvent per audit Event
        │  subject: kape.events.security.audit
        │  id: event.AuditID
        │
        ▼
NATS JetStream (KAPE_EVENTS stream)
        │
        ▼
KapeHandler runtime (subscribes to kape.events.security.audit)
```

### TLS certificate separation

Two distinct TLS certificates are involved in this adapter's lifecycle:

| Cert | Purpose | Secret name | Mount path | This issue |
|---|---|---|---|---|
| Server TLS | HTTPS endpoint — K8s apiserver connects to adapter | `kape-audit-adapter-tls` | `/etc/kape/tls/` | Yes — implemented here |
| NATS client mTLS | Adapter authenticates to NATS | `kape-adapter-cert` | `/etc/kape/nats-certs/` | No — deferred to issue #81 |

In this issue the adapter connects to NATS with a plain (non-mTLS) `nats.Connect` call, identical to the alertmanager adapter pattern. The `kape-audit-adapter-tls` volume is mounted for the server-side HTTPS listener only. The `nats-certs` volume mount is omitted from this issue's Deployment manifest — it will be added in #81.

---

## Design decisions

### D1 — Single NATS subject

Subject is `kape.events.security.audit`. No per-verb or per-resource subject decomposition. Handlers select signals via `trigger.filter.jsonpath` on `$.data.verb`, `$.data.resource`, etc. This is locked by spec 0006 section 5.

### D2 — HTTPS server on :8443

The K8s API server rejects plain HTTP audit webhook backends. The adapter must present a valid TLS certificate. cert-manager issues a `Certificate` resource named `kape-audit-adapter-tls`, signed by the `kape-ca` ClusterIssuer. The resulting Secret is mounted read-only at `/etc/kape/tls/`. The server reads `tls.crt` and `tls.key` from this path.

### D3 — auditID as CloudEvent ID

Each K8s audit event carries a UUID field `auditID`. This is used verbatim as the CloudEvent `id`. Benefits: (a) deduplication — the NATS JetStream `Nats-Msg-Id` header (set by the shared publisher via `jetstream.WithMsgID`) uses this value, so duplicate POSTs within the stream's dedup window are idempotent; (b) correlateability — operators can match a CloudEvent to its entry in API server audit logs using `auditID`.

### D4 — CLUSTER_NAME env var for source field

CloudEvent `source` is `k8s-apiserver/<cluster-name>`. The cluster name is injected via the `CLUSTER_NAME` environment variable, read from the Deployment annotation `kape.io/cluster-name` via `fieldRef`. If `CLUSTER_NAME` is empty, default to `unknown`.

### D5 — Playground flag

A `-playground` flag behaves identically to the alertmanager adapter: construct one synthetic audit event for a `get` on `secrets`, publish it via the handler, log success or failure, then exit. No HTTPS server is started in playground mode.

### D6 — Plain NATS connection in this issue

`nats.Connect` is called without client certificates, matching the current alertmanager adapter. mTLS will be added in issue #81 when the `kape-adapter-cert` mount is introduced across all adapters.

### D7 — No adapter-side filtering

The adapter does not filter events by verb or resource. All events reaching the webhook endpoint have already been filtered by the audit policy (which the API server applies before calling the webhook). The adapter's only job is parse → translate → publish.

### D8 — Endpoint path is /webhook

The HTTP handler is registered at `POST /webhook`, consistent with the alertmanager adapter. The K8s webhook config's `server` URL must include the full path: `https://kape-audit-adapter.kape-system.svc/webhook`.

---

## Work items

### W1 — Audit event types (`adapters/internal/audit/types.go`)

Define Go structs for the K8s audit API:

```go
package audit

import "time"

// EventList is the batched payload POSTed by the K8s API server.
type EventList struct {
    APIVersion string  `json:"apiVersion"` // "audit.k8s.io/v1"
    Kind       string  `json:"kind"`       // "EventList"
    Items      []Event `json:"items"`
}

// Event is a single K8s audit event.
type Event struct {
    AuditID                  string      `json:"auditID"`
    Stage                    string      `json:"stage"`
    RequestURI               string      `json:"requestURI"`
    Verb                     string      `json:"verb"`
    User                     UserInfo    `json:"user"`
    SourceIPs                []string    `json:"sourceIPs,omitempty"`
    UserAgent                string      `json:"userAgent,omitempty"`
    ObjectRef                *ObjectRef  `json:"objectRef,omitempty"`
    ResponseStatus           *StatusInfo `json:"responseStatus,omitempty"`
    RequestObject            *RawObject  `json:"requestObject,omitempty"`
    ResponseObject           *RawObject  `json:"responseObject,omitempty"`
    RequestReceivedTimestamp time.Time   `json:"requestReceivedTimestamp"`
    StageTimestamp           time.Time   `json:"stageTimestamp"`
}

// UserInfo represents the authenticated user on the request.
type UserInfo struct {
    Username string   `json:"username"`
    UID      string   `json:"uid,omitempty"`
    Groups   []string `json:"groups,omitempty"`
}

// ObjectRef is the Kubernetes object the request targeted.
type ObjectRef struct {
    Resource        string `json:"resource,omitempty"`
    Namespace       string `json:"namespace,omitempty"`
    Name            string `json:"name,omitempty"`
    APIVersion      string `json:"apiVersion,omitempty"`
    APIGroup        string `json:"apiGroup,omitempty"`
    ResourceVersion string `json:"resourceVersion,omitempty"`
    Subresource     string `json:"subresource,omitempty"`
}

// StatusInfo holds the HTTP response status.
type StatusInfo struct {
    Code   int32  `json:"code"`
    Status string `json:"status,omitempty"`
}

// RawObject is an arbitrary JSON object (request/response body).
// Stored as json.RawMessage to avoid re-encoding cost.
type RawObject struct {
    Raw json.RawMessage `json:"raw,omitempty"`
}
```

### W2 — CloudEvent builder input type (`adapters/internal/audit/builder.go`)

Add `AuditInput` and `BuildAudit` to the audit package (not the shared `cloudevents` package — the existing `cloudevents.Build` is AlertManager-specific):

```go
package audit

import (
    ce "github.com/cloudevents/sdk-go/v2"
)

const subject = "kape.events.security.audit"

// EventData is the structured payload stored in the CloudEvent `data` field.
type EventData struct {
    Verb           string          `json:"verb"`
    Resource       string          `json:"resource"`
    Subresource    string          `json:"subresource,omitempty"`
    Namespace      string          `json:"namespace,omitempty"`
    Name           string          `json:"name,omitempty"`
    User           UserInfo        `json:"user"`
    UserAgent      string          `json:"userAgent,omitempty"`
    ResponseCode   int32           `json:"responseCode"`
    RequestObject  json.RawMessage `json:"requestObject"`
    ResponseObject json.RawMessage `json:"responseObject"`
    Stage          string          `json:"stage"`
    SourceIPs      []string        `json:"sourceIPs,omitempty"`
}

// BuildAudit constructs a CloudEvents 1.0 event from a K8s audit Event.
// clusterName is the value of the CLUSTER_NAME env var.
// Returns error only if SetData fails (JSON marshal of EventData).
func BuildAudit(ev Event, clusterName string) (ce.Event, error)
```

Implementation notes for `BuildAudit`:

- `event.SetID(ev.AuditID)`
- `event.SetType(subject)`
- `event.SetSource(fmt.Sprintf("k8s-apiserver/%s", clusterName))`
- `event.SetTime(ev.RequestReceivedTimestamp)`
- `event.SetDataContentType("application/json")`
- `ObjectRef` fields are read safely — `ObjectRef` may be nil (non-resource requests); use empty strings in that case
- `ResponseCode` comes from `ResponseStatus.Code`; if `ResponseStatus` is nil, use 0
- `RequestObject` and `ResponseObject`: if the field is nil, encode as JSON `null`

### W3 — HTTP handler (`adapters/internal/audit/handler.go`)

```go
package audit

// Handler processes K8s audit webhook payloads.
type Handler struct {
    publisher       Publisher
    logger          zerolog.Logger
    publishTTL      time.Duration
    clusterName     string
    eventsReceived  prometheus.Counter
    eventsPublished prometheus.Counter
    publishErrors   prometheus.Counter
}

// Publisher is the interface the Handler uses to emit CloudEvents.
// (Identical interface as in the alertmanager package — copied here to keep packages independent.)
type Publisher interface {
    Publish(ctx context.Context, subject string, event ce.Event) error
}

// NewHandler creates a Handler and registers Prometheus metrics.
func NewHandler(pub Publisher, logger zerolog.Logger, publishTTL time.Duration, clusterName string) *Handler

// ServeHTTP processes POST /webhook.
// Returns 200 OK after processing all events (even if some publishes fail — individual failures are logged).
// Returns 400 Bad Request if the body is not valid JSON or Kind != "EventList".
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request)
```

Prometheus metric names:

| Metric | Type | Description |
|---|---|---|
| `kape_audit_events_received_total` | Counter | Total individual audit events received from the API server |
| `kape_audit_events_published_total` | Counter | Total CloudEvents successfully published to NATS |
| `kape_audit_publish_errors_total` | Counter | Total NATS publish failures after retry TTL |

Per-event processing in `ServeHTTP`:

1. Decode body as `audit.EventList`; return 400 if decode fails
2. For each `Event` in `Items`: increment `eventsReceived`
3. Call `BuildAudit(event, h.clusterName)`
4. `ctx, cancel := context.WithTimeout(context.Background(), h.publishTTL)`
5. `pubErr := h.publisher.Publish(ctx, subject, ce)`; cancel after
6. On error: increment `publishErrors`, log at Error with `audit_id`, `verb`, `resource`, full CloudEvent JSON; continue
7. On success: increment `eventsPublished`, log at Info with `audit_id`, `verb`, `resource`
8. After all events: `w.WriteHeader(http.StatusOK)`

### W4 — Main entrypoint (`adapters/cmd/audit/main.go`)

Replaces the current stub. Structure mirrors `adapters/cmd/alertmanager/main.go`:

```go
package main

func main() {
    log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

    playground := flag.Bool("playground", false, "Publish one sample audit event to NATS and exit")
    flag.Parse()

    natsURL     := envOr("NATS_URL", natsgo.DefaultURL)
    port        := envOr("PORT", "8443")
    publishTTL  := envDuration("PUBLISH_TIMEOUT_SECONDS", 60)
    clusterName := envOr("CLUSTER_NAME", "unknown")
    tlsCertFile := envOr("TLS_CERT_FILE", "/etc/kape/tls/tls.crt")
    tlsKeyFile  := envOr("TLS_KEY_FILE",  "/etc/kape/tls/tls.key")

    nc, err := natsgo.Connect(natsURL,
        natsgo.Name("kape-audit-adapter"),
        natsgo.MaxReconnects(-1),
        natsgo.ReconnectWait(2*time.Second),
    )
    // ... fatal on error

    publisher, err := natspkg.NewPublisher(nc)
    // ... fatal on error

    if *playground {
        runPlayground(publisher, publishTTL, clusterName)
        return
    }

    handler := audit.NewHandler(publisher, log.Logger, publishTTL, clusterName)

    r := chi.NewRouter()
    r.Use(middleware.RequestID)
    r.Use(middleware.Recoverer)
    r.Post("/webhook", handler.ServeHTTP)
    r.Get("/healthz", healthzHandler(nc))
    r.Handle("/metrics", promhttp.Handler())

    addr := fmt.Sprintf(":%s", port)
    log.Info().Str("addr", addr).Str("cluster_name", clusterName).Msg("kape-audit-adapter starting")

    srv := &http.Server{
        Addr:    addr,
        Handler: r,
    }
    if err := srv.ListenAndServeTLS(tlsCertFile, tlsKeyFile); err != nil {
        log.Fatal().Err(err).Msg("server exited")
    }
}
```

`healthzHandler` returns 200 when `nc.IsConnected()`, 503 otherwise — identical to alertmanager.

`runPlayground` constructs a synthetic `audit.Event`:

```go
func runPlayground(publisher *natspkg.Publisher, ttl time.Duration, clusterName string) {
    now := time.Now().UTC()
    ev := audit.Event{
        AuditID: "playground-" + uuid.New().String(),
        Stage:   "ResponseComplete",
        Verb:    "get",
        User: audit.UserInfo{
            Username: "system:serviceaccount:prod:api",
            Groups:   []string{"system:serviceaccounts"},
        },
        UserAgent: "kubectl/v1.35.0 (linux/amd64)",
        ObjectRef: &audit.ObjectRef{
            Resource:  "secrets",
            Namespace: "prod",
            Name:      "db-credentials",
        },
        ResponseStatus:           &audit.StatusInfo{Code: 200},
        RequestReceivedTimestamp: now,
        StageTimestamp:           now,
    }
    // build CloudEvent, publish via handler.ServeHTTP using httptest.NewRecorder, log result
}
```

### W5 — cert-manager Certificate manifest (`deploy/helm/kape/templates/audit-adapter-tls-cert.yaml`)

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kape-audit-adapter-tls
  namespace: kape-system
spec:
  secretName: kape-audit-adapter-tls
  issuerRef:
    name: kape-ca
    kind: ClusterIssuer
  dnsNames:
    - kape-audit-adapter.kape-system.svc
    - kape-audit-adapter.kape-system.svc.cluster.local
  duration: 8760h   # 1 year
  renewBefore: 720h # 30 days before expiry
```

### W6 — Kubernetes Deployment manifest (`deploy/helm/kape/templates/audit-adapter-deployment.yaml`)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kape-audit-adapter
  namespace: kape-system
  annotations:
    kape.io/cluster-name: "{{ .Values.clusterName }}"
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kape-audit-adapter
  template:
    metadata:
      labels:
        app: kape-audit-adapter
      annotations:
        kape.io/cluster-name: "{{ .Values.clusterName }}"
    spec:
      containers:
        - name: adapter
          image: "{{ .Values.auditAdapter.image }}:{{ .Values.auditAdapter.tag }}"
          ports:
            - containerPort: 8443
              name: https
            - containerPort: 9090
              name: metrics
          env:
            - name: NATS_URL
              value: "nats://nats.kape-system.svc:4222"
            - name: CLUSTER_NAME
              valueFrom:
                fieldRef:
                  fieldPath: "metadata.annotations['kape.io/cluster-name']"
            - name: PUBLISH_TIMEOUT_SECONDS
              value: "60"
            - name: TLS_CERT_FILE
              value: /etc/kape/tls/tls.crt
            - name: TLS_KEY_FILE
              value: /etc/kape/tls/tls.key
          volumeMounts:
            - name: tls-certs
              mountPath: /etc/kape/tls
              readOnly: true
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8443
              scheme: HTTPS
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8443
              scheme: HTTPS
            initialDelaySeconds: 10
            periodSeconds: 30
      volumes:
        - name: tls-certs
          secret:
            secretName: kape-audit-adapter-tls
```

Note: the `nats-certs` volume is intentionally absent — it will be added in issue #81.

### W7 — Kubernetes Service manifest (`deploy/helm/kape/templates/audit-adapter-service.yaml`)

```yaml
apiVersion: v1
kind: Service
metadata:
  name: kape-audit-adapter
  namespace: kape-system
spec:
  selector:
    app: kape-audit-adapter
  ports:
    - name: https
      port: 443
      targetPort: 8443
    - name: metrics
      port: 9090
      targetPort: 9090
```

The Service exposes port 443 (mapped to container 8443) so the K8s webhook config URL is `https://kape-audit-adapter.kape-system.svc/webhook` without a non-standard port.

### W8 — Audit policy manifest (`examples/audit-policy/kape-audit-policy.yaml`)

New file. Verbatim from spec 0006 section 9. Ship as an example, not as a Helm-managed resource — the audit policy must be applied to the API server configuration, not via Helm:

```yaml
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
  - level: RequestResponse
    resources:
      - group: ""
        resources: ["secrets"]
    verbs: ["get", "create", "update", "patch", "delete"]

  - level: RequestResponse
    resources:
      - group: ""
        resources: ["pods"]
    verbs: ["create"]

  - level: RequestResponse
    resources:
      - group: "rbac.authorization.k8s.io"
        resources:
          - clusterrolebindings
          - rolebindings
          - clusterroles
          - roles
    verbs: ["create", "update", "patch", "delete"]

  - level: Request
    resources:
      - group: ""
        resources: ["pods/exec", "pods/portforward", "pods/attach"]

  - level: Metadata
    resources:
      - group: ""
        resources: ["serviceaccounts/token"]

  - level: None
```

### W9 — API server webhook kubeconfig example (`examples/audit-policy/kape-audit-webhook-config.yaml`)

New file documenting how operators configure the K8s API server to use the adapter:

```yaml
apiVersion: v1
kind: Config
clusters:
  - name: kape-audit
    cluster:
      server: https://kape-audit-adapter.kape-system.svc/webhook
      certificate-authority: /etc/kubernetes/pki/kape-ca.crt
users:
  - name: kape-audit
    user: {}
contexts:
  - name: kape-audit
    context:
      cluster: kape-audit
      user: kape-audit
current-context: kape-audit
```

This kubeconfig is referenced via `--audit-webhook-config-file` on the API server. The `certificate-authority` must be the CA cert for the cert-manager `kape-ca` ClusterIssuer — operators must export it and place it on the API server node.

### W10 — Unit tests (`adapters/internal/audit/handler_test.go`, `adapters/internal/audit/builder_test.go`)

`builder_test.go`:

- `TestBuildAudit_FullEvent`: EventList with `ObjectRef`, `ResponseStatus`, `RequestObject`, `ResponseObject` set — verify all CloudEvent fields
- `TestBuildAudit_NilObjectRef`: `ObjectRef` nil — `resource`, `namespace`, `name` should be empty strings, no panic
- `TestBuildAudit_NilResponseStatus`: `ResponseStatus` nil — `responseCode` should be 0
- `TestBuildAudit_UsesAuditID`: CloudEvent `id` equals the audit event's `auditID`

`handler_test.go`:

- `TestHandler_ValidEventList`: POST valid `EventList` with two events → 200, publisher called twice
- `TestHandler_InvalidJSON`: POST malformed JSON → 400
- `TestHandler_PublishError`: publisher returns error → 200 (individual failures do not fail the request), `publishErrors` counter incremented
- `TestHandler_EmptyItems`: POST `EventList` with `items: []` → 200, publisher not called

Use a mock `Publisher` that captures calls. Use `httptest.NewRecorder` for requests — no TLS needed in unit tests.

### W11 — Remove TODO.md stub (`adapters/cmd/audit/TODO.md`)

Delete the file once `main.go` is written. The implementation supersedes the stub.

---

## Key files

| Path | Status | Description |
|---|---|---|
| `adapters/cmd/audit/main.go` | New | Entrypoint — HTTPS server, NATS connect, playground mode |
| `adapters/cmd/audit/TODO.md` | Delete | Replaced by implementation |
| `adapters/internal/audit/types.go` | New | K8s audit API structs (EventList, Event, UserInfo, ObjectRef, …) |
| `adapters/internal/audit/builder.go` | New | `BuildAudit()` — Event → CloudEvent translation |
| `adapters/internal/audit/handler.go` | New | HTTP handler + Prometheus metrics |
| `adapters/internal/audit/handler_test.go` | New | Handler unit tests |
| `adapters/internal/audit/builder_test.go` | New | Builder unit tests |
| `adapters/internal/cloudevents/builder.go` | Unchanged | Not modified — audit uses its own builder in `internal/audit` |
| `adapters/internal/nats/publisher.go` | Unchanged | Shared — used as-is |
| `deploy/helm/kape/templates/audit-adapter-tls-cert.yaml` | New | cert-manager Certificate for server TLS |
| `deploy/helm/kape/templates/audit-adapter-deployment.yaml` | New | Deployment spec |
| `deploy/helm/kape/templates/audit-adapter-service.yaml` | New | Service (443 → 8443) |
| `examples/audit-policy/kape-audit-policy.yaml` | New | Recommended audit policy YAML |
| `examples/audit-policy/kape-audit-webhook-config.yaml` | New | API server webhook kubeconfig example |

---

## Acceptance criteria

1. `go build ./adapters/cmd/audit/...` succeeds with no errors.
2. `go test ./adapters/internal/audit/...` passes all unit tests.
3. Playground mode: `./kape-audit-adapter -playground` with a running NATS publishes one CloudEvent to `kape.events.security.audit` and exits 0.
4. HTTPS endpoint: `curl -k https://localhost:8443/healthz` returns HTTP 200.
5. End-to-end: POST a synthetic `EventList` containing a `get` on `secrets` to `POST /webhook` — one CloudEvent appears in NATS on `kape.events.security.audit` with `id` equal to the posted `auditID`.
6. Deduplication: POST the same `EventList` twice — NATS JetStream deduplicates (no duplicate consumer delivery) because `Nats-Msg-Id` uses `auditID`.
7. A `KapeHandler` subscribed to `type: kape.events.security.audit` with `filter.jsonpath: $.data.resource` matching `secrets` receives the event and a Task record is written with `status: completed`.
8. Events with subject `kape.events.security.audit` are captured by the `KAPE_EVENTS` stream (subject filter `kape.events.>`).
9. Prometheus `/metrics` exposes `kape_audit_events_received_total`, `kape_audit_events_published_total`, `kape_audit_publish_errors_total`.
10. The deployment's `readinessProbe` passes (HTTPS GET `/healthz` returns 200) once NATS is connected.

---

## Testing strategy

### Unit tests (run in CI)

Located in `adapters/internal/audit/`. Use standard `testing` package and `httptest`. No NATS or Kubernetes dependency.

- Builder tests: cover field mapping, nil safety for `ObjectRef` and `ResponseStatus`, `auditID` → CloudEvent `id` mapping.
- Handler tests: cover valid payload, invalid JSON, publish errors, empty items list. Use a mock `Publisher` interface.

### Integration test (manual / local)

1. Start NATS locally: `podman run -p 4222:4222 nats:latest -js`
2. Run adapter with self-signed cert (generate with `openssl` or `mkcert`):
   ```
   TLS_CERT_FILE=./certs/tls.crt TLS_KEY_FILE=./certs/tls.key \
   CLUSTER_NAME=local-dev \
   go run ./adapters/cmd/audit/
   ```
3. POST a synthetic EventList to `https://localhost:8443/webhook` with `curl -k`
4. Subscribe to NATS and confirm message on `kape.events.security.audit`

### Playground test

```
go run ./adapters/cmd/audit/ -playground
```

Requires a reachable NATS at `NATS_URL` (default `nats://localhost:4222`). Logs published event and exits.

### TLS certificate test (cluster)

Deploy to a dev cluster with cert-manager installed and `kape-ca` ClusterIssuer present. Verify:
- `kubectl get certificate kape-audit-adapter-tls -n kape-system` shows `READY=True`
- Pod starts and readinessProbe passes

### What is NOT tested in this issue

- NATS mTLS client authentication — deferred to #81
- End-to-end API server audit webhook integration — requires a real cluster with `--audit-webhook-config-file` set; document as a manual validation step
