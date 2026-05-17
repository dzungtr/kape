# K8s Audit Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `kape-audit-adapter`: a Go HTTPS service that receives K8s API server audit webhook payloads, converts each audit event into a CloudEvent, and publishes to NATS JetStream on `kape.events.security.audit`.

**Architecture:** chi router serves `POST /webhook` on `:8443` with TLS; each batched `EventList` is parsed and each `Event` translated to a CloudEvent using `auditID` as the CE id and published via the shared `internal/nats.Publisher`. cert-manager issues the server TLS certificate (mounted at `/etc/kape/tls/`). NATS connection is plain (no mTLS — deferred to issue #81).

**Tech Stack:** Go 1.25, go-chi/v5, zerolog, cloudevents/sdk-go/v2, nats.go/jetstream, prometheus/client_golang, testify, net/http/httptest.

**Spec:** `docs/superpowers/specs/2026-05-17-phase8-issue-76-k8s-audit-adapter-design.md`

---

## File Map

| Path | Status | Responsibility |
|---|---|---|
| `adapters/internal/audit/types.go` | Create | K8s audit API structs: EventList, Event, UserInfo, ObjectRef, StatusInfo, RawObject |
| `adapters/internal/audit/builder.go` | Create | `BuildAudit()` — translates audit.Event → CloudEvent; defines EventData |
| `adapters/internal/audit/builder_test.go` | Create | Unit tests for BuildAudit covering nil ObjectRef, nil ResponseStatus, nil objects |
| `adapters/internal/audit/handler.go` | Create | HTTP handler + Prometheus counters; defines Publisher interface |
| `adapters/internal/audit/handler_test.go` | Create | Unit tests for Handler.ServeHTTP (valid, invalid JSON, publish error, empty items) |
| `adapters/cmd/audit/main.go` | Create | Entrypoint — HTTPS server, NATS connect, playground mode |
| `adapters/cmd/audit/TODO.md` | Delete | Replaced by the above implementation |
| `helm/templates/adapters/audit-adapter-tls-cert.yaml` | Create | cert-manager Certificate for server TLS |
| `helm/templates/adapters/audit-adapter-deployment.yaml` | Create | Deployment with TLS volume mount and CLUSTER_NAME env var |
| `helm/templates/adapters/audit-adapter-service.yaml` | Create | Service exposing port 443 → container 8443 |
| `helm/values.yaml` | Modify | Add top-level `clusterName: ""` field |
| `examples/audit-policy/kape-audit-policy.yaml` | Create | Recommended K8s audit policy (shipped as example, not Helm-managed) |
| `examples/audit-policy/kape-audit-webhook-config.yaml` | Create | API server webhook kubeconfig showing how to point at the adapter |

---

## Task 1: Audit event types

**Files:**
- Create: `adapters/internal/audit/types.go`

- [ ] **Step 1: Create `adapters/internal/audit/types.go`**

```go
package audit

import (
	"encoding/json"
	"time"
)

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

- [ ] **Step 2: Verify it compiles**

```bash
cd adapters && go build ./internal/audit/...
```
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add adapters/internal/audit/types.go
git commit -m "feat(adapters/audit): add K8s audit API types (EventList, Event, ObjectRef, …)"
```

---

## Task 2: CloudEvent builder (TDD)

**Files:**
- Create: `adapters/internal/audit/builder_test.go`
- Create: `adapters/internal/audit/builder.go`

- [ ] **Step 1: Write failing tests in `adapters/internal/audit/builder_test.go`**

```go
package audit_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kape-io/kape/adapters/internal/audit"
)

func baseEvent(auditID, verb string) audit.Event {
	return audit.Event{
		AuditID: auditID,
		Stage:   "ResponseComplete",
		Verb:    verb,
		User:    audit.UserInfo{Username: "admin"},
		ObjectRef: &audit.ObjectRef{
			Resource:  "secrets",
			Namespace: "prod",
			Name:      "db-creds",
		},
		ResponseStatus:           &audit.StatusInfo{Code: 200},
		RequestReceivedTimestamp: time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
		StageTimestamp:           time.Date(2026, 5, 17, 12, 0, 1, 0, time.UTC),
	}
}

func TestBuildAudit_FullEvent(t *testing.T) {
	ev := baseEvent("audit-uuid-1234", "create")
	ev.RequestObject = &audit.RawObject{Raw: json.RawMessage(`{"key":"value"}`)}
	ev.ResponseObject = &audit.RawObject{Raw: json.RawMessage(`{"status":"ok"}`)}

	ce, err := audit.BuildAudit(ev, "prod-cluster")
	require.NoError(t, err)

	assert.Equal(t, "audit-uuid-1234", ce.ID())
	assert.Equal(t, "kape.events.security.audit", ce.Type())
	assert.Equal(t, "k8s-apiserver/prod-cluster", ce.Source())
	assert.Equal(t, time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC), ce.Time())
	assert.Equal(t, "application/json", ce.DataContentType())

	var data map[string]any
	require.NoError(t, json.Unmarshal(ce.Data(), &data))
	assert.Equal(t, "create", data["verb"])
	assert.Equal(t, "secrets", data["resource"])
	assert.Equal(t, "prod", data["namespace"])
	assert.Equal(t, "db-creds", data["name"])
	assert.Equal(t, float64(200), data["responseCode"])
	assert.Equal(t, "ResponseComplete", data["stage"])
}

func TestBuildAudit_NilObjectRef(t *testing.T) {
	ev := baseEvent("audit-nil-ref", "list")
	ev.ObjectRef = nil

	ce, err := audit.BuildAudit(ev, "dev-cluster")
	require.NoError(t, err)

	var data map[string]any
	require.NoError(t, json.Unmarshal(ce.Data(), &data))
	assert.Equal(t, "", data["resource"])
	assert.Equal(t, "", data["namespace"])
	assert.Equal(t, "", data["name"])
}

func TestBuildAudit_NilResponseStatus(t *testing.T) {
	ev := baseEvent("audit-nil-status", "delete")
	ev.ResponseStatus = nil

	ce, err := audit.BuildAudit(ev, "dev-cluster")
	require.NoError(t, err)

	var data map[string]any
	require.NoError(t, json.Unmarshal(ce.Data(), &data))
	assert.Equal(t, float64(0), data["responseCode"])
}

func TestBuildAudit_UsesAuditID(t *testing.T) {
	ev := baseEvent("my-specific-audit-id", "get")

	ce, err := audit.BuildAudit(ev, "any-cluster")
	require.NoError(t, err)

	assert.Equal(t, "my-specific-audit-id", ce.ID())
}

func TestBuildAudit_NilRequestResponseObjects(t *testing.T) {
	ev := baseEvent("audit-nil-objects", "get")
	ev.RequestObject = nil
	ev.ResponseObject = nil

	ce, err := audit.BuildAudit(ev, "dev-cluster")
	require.NoError(t, err)

	var data map[string]any
	require.NoError(t, json.Unmarshal(ce.Data(), &data))
	assert.Nil(t, data["requestObject"])
	assert.Nil(t, data["responseObject"])
}
```

- [ ] **Step 2: Run tests — confirm they fail (BuildAudit not defined yet)**

```bash
cd adapters && go test ./internal/audit/... -v -run TestBuildAudit
```
Expected: FAIL with `undefined: audit.BuildAudit`.

- [ ] **Step 3: Create `adapters/internal/audit/builder.go`**

```go
package audit

import (
	"encoding/json"
	"fmt"

	ce "github.com/cloudevents/sdk-go/v2"
)

const subject = "kape.events.security.audit"

// EventData is the structured payload stored in the CloudEvent data field.
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
// clusterName is the value of the CLUSTER_NAME env var; use "unknown" if empty.
func BuildAudit(ev Event, clusterName string) (ce.Event, error) {
	var resource, namespace, name, subresource string
	if ev.ObjectRef != nil {
		resource = ev.ObjectRef.Resource
		namespace = ev.ObjectRef.Namespace
		name = ev.ObjectRef.Name
		subresource = ev.ObjectRef.Subresource
	}

	var responseCode int32
	if ev.ResponseStatus != nil {
		responseCode = ev.ResponseStatus.Code
	}

	reqObj := json.RawMessage("null")
	if ev.RequestObject != nil {
		reqObj = ev.RequestObject.Raw
	}
	respObj := json.RawMessage("null")
	if ev.ResponseObject != nil {
		respObj = ev.ResponseObject.Raw
	}

	data := EventData{
		Verb:           ev.Verb,
		Resource:       resource,
		Subresource:    subresource,
		Namespace:      namespace,
		Name:           name,
		User:           ev.User,
		UserAgent:      ev.UserAgent,
		ResponseCode:   responseCode,
		RequestObject:  reqObj,
		ResponseObject: respObj,
		Stage:          ev.Stage,
		SourceIPs:      ev.SourceIPs,
	}

	event := ce.NewEvent()
	event.SetSpecVersion("1.0")
	event.SetID(ev.AuditID)
	event.SetType(subject)
	event.SetSource(fmt.Sprintf("k8s-apiserver/%s", clusterName))
	event.SetTime(ev.RequestReceivedTimestamp)
	event.SetDataContentType("application/json")

	if err := event.SetData("application/json", data); err != nil {
		return ce.Event{}, fmt.Errorf("setting event data: %w", err)
	}

	return event, nil
}
```

- [ ] **Step 4: Run tests — confirm all 5 pass**

```bash
cd adapters && go test ./internal/audit/... -v -run TestBuildAudit
```
Expected: 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add adapters/internal/audit/builder.go adapters/internal/audit/builder_test.go
git commit -m "feat(adapters/audit): add CloudEvent builder (BuildAudit) with tests"
```

---

## Task 3: HTTP handler (TDD)

**Files:**
- Create: `adapters/internal/audit/handler_test.go`
- Create: `adapters/internal/audit/handler.go`

- [ ] **Step 1: Write failing tests in `adapters/internal/audit/handler_test.go`**

```go
package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kape-io/kape/adapters/internal/audit"
)

type fakePublisher struct {
	published []ce.Event
}

func (f *fakePublisher) Publish(_ context.Context, _ string, event ce.Event) error {
	f.published = append(f.published, event)
	return nil
}

type failingPublisher struct{}

func (f *failingPublisher) Publish(_ context.Context, _ string, _ ce.Event) error {
	return fmt.Errorf("nats unavailable")
}

func makeEventList(events []audit.Event) []byte {
	body, _ := json.Marshal(audit.EventList{
		APIVersion: "audit.k8s.io/v1",
		Kind:       "EventList",
		Items:      events,
	})
	return body
}

func sampleAuditEvent(auditID string) audit.Event {
	now := time.Now().UTC()
	return audit.Event{
		AuditID: auditID,
		Stage:   "ResponseComplete",
		Verb:    "get",
		User:    audit.UserInfo{Username: "admin"},
		ObjectRef: &audit.ObjectRef{
			Resource:  "secrets",
			Namespace: "prod",
			Name:      "db-creds",
		},
		ResponseStatus:           &audit.StatusInfo{Code: 200},
		RequestReceivedTimestamp: now,
		StageTimestamp:           now,
	}
}

func TestHandler_ValidEventList(t *testing.T) {
	pub := &fakePublisher{}
	logger := zerolog.Nop()
	h := audit.NewHandler(pub, logger, 60*time.Second, "test-cluster")

	events := []audit.Event{
		sampleAuditEvent("audit-id-1"),
		sampleAuditEvent("audit-id-2"),
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(makeEventList(events)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, pub.published, 2)
	assert.Equal(t, "audit-id-1", pub.published[0].ID())
	assert.Equal(t, "audit-id-2", pub.published[1].ID())
	assert.Equal(t, "kape.events.security.audit", pub.published[0].Type())
}

func TestHandler_InvalidJSON(t *testing.T) {
	pub := &fakePublisher{}
	logger := zerolog.Nop()
	h := audit.NewHandler(pub, logger, 60*time.Second, "test-cluster")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Empty(t, pub.published)
}

func TestHandler_PublishError(t *testing.T) {
	pub := &failingPublisher{}
	logger := zerolog.Nop()
	h := audit.NewHandler(pub, logger, 60*time.Second, "test-cluster")

	events := []audit.Event{sampleAuditEvent("audit-id-fail")}

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(makeEventList(events)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	// Publish failure is logged but must not fail the HTTP response.
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestHandler_EmptyItems(t *testing.T) {
	pub := &fakePublisher{}
	logger := zerolog.Nop()
	h := audit.NewHandler(pub, logger, 60*time.Second, "test-cluster")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(makeEventList(nil)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, pub.published)
}
```

- [ ] **Step 2: Run tests — confirm they fail (NewHandler not defined yet)**

```bash
cd adapters && go test ./internal/audit/... -v -run TestHandler
```
Expected: FAIL with `undefined: audit.NewHandler`.

- [ ] **Step 3: Create `adapters/internal/audit/handler.go`**

```go
package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

// Publisher is the interface the Handler uses to emit CloudEvents.
type Publisher interface {
	Publish(ctx context.Context, subject string, event ce.Event) error
}

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

// NewHandler creates a Handler and registers Prometheus metrics.
func NewHandler(pub Publisher, logger zerolog.Logger, publishTTL time.Duration, clusterName string) *Handler {
	reg := prometheus.DefaultRegisterer
	eventsReceived := mustOrExisting(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kape_audit_events_received_total",
		Help: "Total individual audit events received from the API server.",
	}))
	eventsPublished := mustOrExisting(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kape_audit_events_published_total",
		Help: "Total CloudEvents successfully published to NATS.",
	}))
	publishErrors := mustOrExisting(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kape_audit_publish_errors_total",
		Help: "Total NATS publish failures after retry TTL.",
	}))

	return &Handler{
		publisher:       pub,
		logger:          logger,
		publishTTL:      publishTTL,
		clusterName:     clusterName,
		eventsReceived:  eventsReceived,
		eventsPublished: eventsPublished,
		publishErrors:   publishErrors,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var list EventList
	if err := json.NewDecoder(r.Body).Decode(&list); err != nil {
		h.logger.Warn().Err(err).Msg("failed to decode audit EventList")
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	for _, ev := range list.Items {
		h.eventsReceived.Inc()

		cloudEvent, err := BuildAudit(ev, h.clusterName)
		if err != nil {
			h.logger.Error().Err(err).Str("audit_id", ev.AuditID).Msg("failed to build CloudEvent")
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), h.publishTTL)
		pubErr := h.publisher.Publish(ctx, subject, cloudEvent)
		cancel()

		if pubErr != nil {
			h.publishErrors.Inc()
			h.logger.Error().Err(pubErr).
				Str("audit_id", ev.AuditID).
				Str("verb", ev.Verb).
				Str("resource", resourceOf(ev)).
				RawJSON("event", mustMarshal(cloudEvent)).
				Msg("dropped audit event: publish failed after retry TTL")
			continue
		}

		h.eventsPublished.Inc()
		h.logger.Info().
			Str("audit_id", ev.AuditID).
			Str("verb", ev.Verb).
			Str("resource", resourceOf(ev)).
			Msg("published audit CloudEvent")
	}

	w.WriteHeader(http.StatusOK)
}

func resourceOf(ev Event) string {
	if ev.ObjectRef != nil {
		return ev.ObjectRef.Resource
	}
	return ""
}

func mustOrExisting(reg prometheus.Registerer, c prometheus.Counter) prometheus.Counter {
	if err := reg.Register(c); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(prometheus.Counter)
		}
		panic(err)
	}
	return c
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
```

- [ ] **Step 4: Run all audit package tests — confirm all 9 pass**

```bash
cd adapters && go test ./internal/audit/... -v
```
Expected: 9 tests PASS (5 from `builder_test.go` + 4 from `handler_test.go`).

- [ ] **Step 5: Commit**

```bash
git add adapters/internal/audit/handler.go adapters/internal/audit/handler_test.go
git commit -m "feat(adapters/audit): add HTTP handler with Prometheus metrics and tests"
```

---

## Task 4: Main entrypoint

**Files:**
- Create: `adapters/cmd/audit/main.go`
- Delete: `adapters/cmd/audit/TODO.md`

- [ ] **Step 1: Create `adapters/cmd/audit/main.go`**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	natsgo "github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/kape-io/kape/adapters/internal/audit"
	natspkg "github.com/kape-io/kape/adapters/internal/nats"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	playground := flag.Bool("playground", false, "Publish one sample audit event to NATS and exit")
	flag.Parse()

	natsURL     := envOr("NATS_URL", natsgo.DefaultURL)
	port        := envOr("PORT", "8443")
	publishTTL  := envDuration("PUBLISH_TIMEOUT_SECONDS", 60)
	clusterName := envOr("CLUSTER_NAME", "unknown")
	tlsCertFile := envOr("TLS_CERT_FILE", "/etc/kape/tls/tls.crt")
	tlsKeyFile  := envOr("TLS_KEY_FILE", "/etc/kape/tls/tls.key")

	nc, err := natsgo.Connect(natsURL,
		natsgo.Name("kape-audit-adapter"),
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(2*time.Second),
	)
	if err != nil {
		log.Fatal().Err(err).Str("nats_url", natsURL).Msg("failed to connect to NATS")
	}
	defer nc.Drain()
	log.Info().Str("nats_url", natsURL).Msg("connected to NATS")

	publisher, err := natspkg.NewPublisher(nc)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialise NATS publisher")
	}
	log.Info().Msg("KAPE_EVENTS stream provisioned")

	if *playground {
		runPlayground(publisher, publishTTL, clusterName)
		return
	}

	handler := audit.NewHandler(publisher, log.Logger, publishTTL, clusterName)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Post("/webhook", handler.ServeHTTP)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if nc.IsConnected() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
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

	list := audit.EventList{
		APIVersion: "audit.k8s.io/v1",
		Kind:       "EventList",
		Items:      []audit.Event{ev},
	}

	body, err := json.Marshal(list)
	if err != nil {
		log.Fatal().Err(err).Msg("marshal playground payload")
	}

	h := audit.NewHandler(publisher, log.Logger, ttl, clusterName)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		log.Fatal().Int("status", rr.Code).Str("body", rr.Body.String()).Msg("playground publish failed")
	}
	log.Info().Str("audit_id", ev.AuditID).Msg("playground: published audit event to NATS")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, defaultSeconds int) time.Duration {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return time.Duration(defaultSeconds) * time.Second
}
```

- [ ] **Step 2: Delete the TODO stub**

```bash
git rm adapters/cmd/audit/TODO.md
```

- [ ] **Step 3: Verify the binary builds**

```bash
cd adapters && go build ./cmd/audit/...
```
Expected: no output, exit 0.

- [ ] **Step 4: Run all adapter tests to confirm nothing broke**

```bash
cd adapters && go test ./...
```
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add adapters/cmd/audit/main.go
git commit -m "feat(adapters/audit): implement main entrypoint — HTTPS server, NATS connect, playground mode"
```

---

## Task 5: Helm templates

**Files:**
- Modify: `helm/values.yaml`
- Create: `helm/templates/adapters/audit-adapter-tls-cert.yaml`
- Create: `helm/templates/adapters/audit-adapter-deployment.yaml`
- Create: `helm/templates/adapters/audit-adapter-service.yaml`

- [ ] **Step 1: Add `clusterName` to `helm/values.yaml`**

Append the following line at the end of `helm/values.yaml`:

```yaml
clusterName: ""
```

- [ ] **Step 2: Create `helm/templates/adapters/audit-adapter-tls-cert.yaml`**

```yaml
{{- if .Values.adapters.audit.enabled }}
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kape-audit-adapter-tls
  namespace: {{ .Values.namespace }}
spec:
  secretName: kape-audit-adapter-tls
  issuerRef:
    name: kape-ca
    kind: ClusterIssuer
  dnsNames:
    - kape-audit-adapter.{{ .Values.namespace }}.svc
    - kape-audit-adapter.{{ .Values.namespace }}.svc.cluster.local
  duration: 8760h
  renewBefore: 720h
{{- end }}
```

- [ ] **Step 3: Create `helm/templates/adapters/audit-adapter-deployment.yaml`**

```yaml
{{- if .Values.adapters.audit.enabled }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kape-audit-adapter
  namespace: {{ .Values.namespace }}
  annotations:
    kape.io/cluster-name: {{ .Values.clusterName | quote }}
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
        kape.io/cluster-name: {{ .Values.clusterName | quote }}
    spec:
      containers:
        - name: adapter
          image: "{{ .Values.adapters.audit.image.repository }}:{{ .Values.adapters.audit.image.tag }}"
          ports:
            - containerPort: 8443
              name: https
            - containerPort: 9090
              name: metrics
          env:
            - name: NATS_URL
              value: "nats://nats.{{ .Values.namespace }}.svc:4222"
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
{{- end }}
```

- [ ] **Step 4: Create `helm/templates/adapters/audit-adapter-service.yaml`**

```yaml
{{- if .Values.adapters.audit.enabled }}
apiVersion: v1
kind: Service
metadata:
  name: kape-audit-adapter
  namespace: {{ .Values.namespace }}
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
{{- end }}
```

- [ ] **Step 5: Verify Helm templates render cleanly**

```bash
helm template kape ./helm --set adapters.audit.enabled=true --set clusterName=test-cluster | grep -E "^(kind:|  name: kape-audit)"
```
Expected output contains (in any order):
```
kind: Certificate
  name: kape-audit-adapter-tls
kind: Deployment
  name: kape-audit-adapter
kind: Service
  name: kape-audit-adapter
```

- [ ] **Step 6: Run helm lint**

```bash
helm lint ./helm
```
Expected: `1 chart(s) linted, 0 chart(s) failed`.

- [ ] **Step 7: Commit**

```bash
git add helm/values.yaml \
        helm/templates/adapters/audit-adapter-tls-cert.yaml \
        helm/templates/adapters/audit-adapter-deployment.yaml \
        helm/templates/adapters/audit-adapter-service.yaml
git commit -m "feat(helm): add kape-audit-adapter Deployment, Service, and cert-manager Certificate"
```

---

## Task 6: Example files

**Files:**
- Create: `examples/audit-policy/kape-audit-policy.yaml`
- Create: `examples/audit-policy/kape-audit-webhook-config.yaml`

- [ ] **Step 1: Create `examples/audit-policy/kape-audit-policy.yaml`**

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

- [ ] **Step 2: Create `examples/audit-policy/kape-audit-webhook-config.yaml`**

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

- [ ] **Step 3: Commit**

```bash
git add examples/audit-policy/kape-audit-policy.yaml \
        examples/audit-policy/kape-audit-webhook-config.yaml
git commit -m "docs(examples): add K8s audit policy and webhook kubeconfig for kape-audit-adapter"
```

---

## Task 7: Final verification

- [ ] **Step 1: Full build**

```bash
cd adapters && go build ./...
```
Expected: exit 0.

- [ ] **Step 2: Full test run**

```bash
cd adapters && go test ./...
```
Expected: all tests PASS, including `ok  github.com/kape-io/kape/adapters/internal/audit`.

- [ ] **Step 3: Binary compile check (acceptance criterion 1)**

```bash
cd adapters && go build -o /tmp/kape-audit-adapter ./cmd/audit/... && echo "BUILD OK"
```
Expected: `BUILD OK`.

- [ ] **Step 4: Unit tests with verbose output (acceptance criterion 2)**

```bash
cd adapters && go test ./internal/audit/... -v -count=1
```
Expected: 9 tests pass — `TestBuildAudit_FullEvent`, `TestBuildAudit_NilObjectRef`, `TestBuildAudit_NilResponseStatus`, `TestBuildAudit_UsesAuditID`, `TestBuildAudit_NilRequestResponseObjects`, `TestHandler_ValidEventList`, `TestHandler_InvalidJSON`, `TestHandler_PublishError`, `TestHandler_EmptyItems`.

- [ ] **Step 5: Helm lint final check**

```bash
helm lint ./helm
```
Expected: `1 chart(s) linted, 0 chart(s) failed`.
