# SRE AlertManager Example Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a fully deployable Kubernetes example that demonstrates the end-to-end KAPE pipeline: a mock-api with random failures, a load-generator driving traffic, SigNoz observing error rates and firing AlertManager alerts, the KAPE alertmanager adapter ingesting into NATS, and a KapeHandler reasoning over alerts using pod logs as evidence before posting a structured SRE decision to a mock webhook receiver.

**Architecture:** Layered kustomize directory structure under `examples/sre-alertmanager/`. Two small Go programs (`mock-api`, `mock-webhook-receiver`) with their own `go.mod` and Dockerfiles under `src/`. All example k8s resources deploy to the `kape-examples` namespace. SigNoz runs in `platform` namespace.

**Tech Stack:** Go 1.23, Kubernetes manifests (YAML), Kustomize, Prometheus/AlertManager (via SigNoz Helm), KAPE CRDs (KapeSchema/KapeTool/KapeHandler), `github.com/prometheus/client_golang`, `curlimages/curl` for load-generator.

**Worktree:** `/home/tony/projects/kape-io/.worktrees/feature/sre-alertmanager-example`

---

## Key Design Decisions

- **`kape_subject` label**: AlertManager alerts must carry label `kape_subject: kape.events.alertmanager.mock-api-errors`. The alertmanager adapter reads this label and uses it as both the NATS subject and the CloudEvent type.
- **KapeHandler trigger**: `source: kape.events.alertmanager.mock-api-errors`, `type: kape.events.alertmanager.mock-api-errors`
- **k8s-mcp**: The KapeTool for log reading references `http://k8s-mcp-svc.kape-system:8080` — already deployed by KAPE Helm. This is a prerequisite.
- **Images**: mock-api and mock-webhook images are referenced as `ghcr.io/kape-io/kape-mock-api:latest` and `ghcr.io/kape-io/kape-mock-webhook:latest`. README explains how to build locally with `imagePullPolicy: Never` for kind/minikube.
- **Load-generator**: Uses `curlimages/curl:8` — no custom image needed.

---

## File Map

```
examples/sre-alertmanager/
├── README.md                              (Task 9)
├── kustomization.yaml                     (Task 1)
├── 00-signoz/
│   ├── README.md                          (Task 8)
│   └── alertmanager-receiver-patch.yaml   (Task 8)
├── 01-mock-api/
│   ├── deployment.yaml                    (Task 3)
│   ├── service.yaml                       (Task 3)
│   ├── service-monitor.yaml               (Task 3)
│   ├── prometheus-rule.yaml               (Task 3)
│   └── kustomization.yaml                 (Task 3)
├── 02-load-generator/
│   ├── deployment.yaml                    (Task 4)
│   └── kustomization.yaml                 (Task 4)
├── 03-kape/
│   ├── kape-schema.yaml                   (Task 7)
│   ├── kape-tool-log-reader.yaml          (Task 7)
│   ├── kape-handler.yaml                  (Task 7)
│   └── kustomization.yaml                 (Task 7)
├── 04-mock-webhook/
│   ├── deployment.yaml                    (Task 6)
│   ├── service.yaml                       (Task 6)
│   └── kustomization.yaml                 (Task 6)
└── src/
    ├── mock-api/
    │   ├── go.mod                         (Task 2)
    │   ├── go.sum                         (Task 2)
    │   ├── main.go                        (Task 2)
    │   ├── handler.go                     (Task 2)
    │   ├── handler_test.go                (Task 2)
    │   └── Dockerfile                     (Task 2)
    └── mock-webhook/
        ├── go.mod                         (Task 5)
        ├── go.sum                         (Task 5)
        ├── main.go                        (Task 5)
        ├── receiver.go                    (Task 5)
        ├── receiver_test.go               (Task 5)
        └── Dockerfile                     (Task 5)
```

---

## Task 1: Directory scaffolding + namespace

**Files:**
- Create: `examples/sre-alertmanager/kustomization.yaml`
- Create: `examples/sre-alertmanager/namespace.yaml`

- [ ] **Step 1: Create directory tree**

```bash
cd /home/tony/projects/kape-io/.worktrees/feature/sre-alertmanager-example
mkdir -p examples/sre-alertmanager/{00-signoz,01-mock-api,02-load-generator,03-kape,04-mock-webhook,src/mock-api,src/mock-webhook}
```

- [ ] **Step 2: Create namespace resource**

Create `examples/sre-alertmanager/namespace.yaml`:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: kape-examples
```

- [ ] **Step 3: Create top-level kustomization**

Create `examples/sre-alertmanager/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - namespace.yaml
  - 01-mock-api/kustomization.yaml
  - 02-load-generator/kustomization.yaml
  - 03-kape/kustomization.yaml
  - 04-mock-webhook/kustomization.yaml
```

- [ ] **Step 4: Commit**

```bash
git add examples/sre-alertmanager/
git commit -m "feat(examples): scaffold sre-alertmanager directory structure"
```

---

## Task 2: mock-api Go source

**Files:**
- Create: `examples/sre-alertmanager/src/mock-api/go.mod`
- Create: `examples/sre-alertmanager/src/mock-api/handler.go`
- Create: `examples/sre-alertmanager/src/mock-api/handler_test.go`
- Create: `examples/sre-alertmanager/src/mock-api/main.go`
- Create: `examples/sre-alertmanager/src/mock-api/Dockerfile`

- [ ] **Step 1: Write the failing test**

Create `examples/sre-alertmanager/src/mock-api/handler_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestAPIHandler_AlwaysSuccess(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := newAPIHandler(0.0, reg) // 0% failure rate
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 got %d", rec.Code)
		}
	}
}

func TestAPIHandler_AlwaysFail(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := newAPIHandler(1.0, reg) // 100% failure rate
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 got %d", rec.Code)
		}
	}
}

func TestAPIHandler_PartialFailure(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := newAPIHandler(0.5, reg)
	successes, failures := 0, 0
	for i := 0; i < 1000; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			successes++
		} else {
			failures++
		}
	}
	// With 50% rate and 1000 samples, expect 350-650 failures
	if failures < 350 || failures > 650 {
		t.Errorf("expected ~500 failures, got %d out of 1000", failures)
	}
}

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	http.HandlerFunc(healthzHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run test — verify it fails**

```bash
cd examples/sre-alertmanager/src/mock-api
go test ./... 2>&1 | head -20
```

Expected: `FAIL` — `newAPIHandler undefined`, `healthzHandler undefined`

- [ ] **Step 3: Initialize go module**

```bash
cd examples/sre-alertmanager/src/mock-api
go mod init github.com/kape-io/kape/examples/mock-api
go get github.com/prometheus/client_golang@v1.22.0
go mod tidy
```

- [ ] **Step 4: Write handler.go**

Create `examples/sre-alertmanager/src/mock-api/handler.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var errorReasons = []string{"db_timeout", "upstream_unavailable", "nil_pointer"}

type requestLog struct {
	RequestID   string `json:"request_id"`
	StatusCode  int    `json:"status_code"`
	LatencyMs   int64  `json:"latency_ms"`
	ErrorReason string `json:"error_reason,omitempty"`
	Timestamp   string `json:"timestamp"`
}

type apiHandler struct {
	failureRate    float64
	requestsTotal  *prometheus.CounterVec
	latencyHist    prometheus.Histogram
}

func newAPIHandler(failureRate float64, reg prometheus.Registerer) *apiHandler {
	factory := promauto.With(reg)
	return &apiHandler{
		failureRate: failureRate,
		requestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "mock_api_requests_total",
			Help: "Total requests partitioned by status.",
		}, []string{"status"}),
		latencyHist: factory.NewHistogram(prometheus.HistogramOpts{
			Name:    "mock_api_latency_seconds",
			Help:    "Request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}),
	}
}

func (h *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := fmt.Sprintf("%d", start.UnixNano())

	log := requestLog{
		RequestID: reqID,
		Timestamp: start.UTC().Format(time.RFC3339),
	}

	if rand.Float64() < h.failureRate {
		reason := errorReasons[rand.Intn(len(errorReasons))]
		log.StatusCode = http.StatusInternalServerError
		log.ErrorReason = reason
		log.LatencyMs = time.Since(start).Milliseconds()

		h.requestsTotal.WithLabelValues("error").Inc()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": reason})
	} else {
		log.StatusCode = http.StatusOK
		log.LatencyMs = time.Since(start).Milliseconds()

		h.requestsTotal.WithLabelValues("success").Inc()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}

	h.latencyHist.Observe(float64(log.LatencyMs) / 1000.0)
	enc := json.NewEncoder(w)
	_ = enc
	// Write structured log to stdout for kubectl logs evidence
	logLine, _ := json.Marshal(log)
	fmt.Println(string(logLine))
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 5: Write main.go**

Create `examples/sre-alertmanager/src/mock-api/main.go`:

```go
package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	failureRate := 0.3
	if v := os.Getenv("FAILURE_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			failureRate = f
		}
	}
	port := envOr("PORT", "8080")

	reg := prometheus.DefaultRegisterer
	h := newAPIHandler(failureRate, reg)

	mux := http.NewServeMux()
	mux.Handle("/api/status", h)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", healthzHandler)

	fmt.Printf(`{"msg":"mock-api starting","port":%q,"failure_rate":%f}`+"\n", port, failureRate)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		fmt.Fprintf(os.Stderr, `{"msg":"server error","error":%q}`+"\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 6: Run tests — verify they pass**

```bash
cd examples/sre-alertmanager/src/mock-api
go test ./... -v 2>&1
```

Expected:
```
--- PASS: TestAPIHandler_AlwaysSuccess (0.00s)
--- PASS: TestAPIHandler_AlwaysFail (0.00s)
--- PASS: TestAPIHandler_PartialFailure (0.00s)
--- PASS: TestHealthz (0.00s)
PASS
```

- [ ] **Step 7: Write Dockerfile**

Create `examples/sre-alertmanager/src/mock-api/Dockerfile`:

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mock-api .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/mock-api /mock-api
ENTRYPOINT ["/mock-api"]
```

- [ ] **Step 8: Commit**

```bash
git add examples/sre-alertmanager/src/mock-api/
git commit -m "feat(examples): add mock-api Go source with handler tests"
```

---

## Task 3: mock-api Kubernetes manifests

**Files:**
- Create: `examples/sre-alertmanager/01-mock-api/deployment.yaml`
- Create: `examples/sre-alertmanager/01-mock-api/service.yaml`
- Create: `examples/sre-alertmanager/01-mock-api/service-monitor.yaml`
- Create: `examples/sre-alertmanager/01-mock-api/prometheus-rule.yaml`
- Create: `examples/sre-alertmanager/01-mock-api/kustomization.yaml`

- [ ] **Step 1: Create deployment.yaml**

Create `examples/sre-alertmanager/01-mock-api/deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mock-api
  namespace: kape-examples
  labels:
    app: mock-api
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mock-api
  template:
    metadata:
      labels:
        app: mock-api
    spec:
      containers:
        - name: mock-api
          image: ghcr.io/kape-io/kape-mock-api:latest
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: 8080
          env:
            - name: FAILURE_RATE
              value: "0.4"
            - name: PORT
              value: "8080"
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 3
            periodSeconds: 5
          resources:
            requests:
              cpu: 50m
              memory: 32Mi
            limits:
              cpu: 200m
              memory: 64Mi
```

- [ ] **Step 2: Create service.yaml**

Create `examples/sre-alertmanager/01-mock-api/service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: mock-api
  namespace: kape-examples
  labels:
    app: mock-api
spec:
  selector:
    app: mock-api
  ports:
    - name: http
      port: 8080
      targetPort: 8080
```

- [ ] **Step 3: Create service-monitor.yaml**

Create `examples/sre-alertmanager/01-mock-api/service-monitor.yaml`:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: mock-api
  namespace: kape-examples
  labels:
    app: mock-api
    release: signoz
spec:
  selector:
    matchLabels:
      app: mock-api
  endpoints:
    - port: http
      path: /metrics
      interval: 15s
```

- [ ] **Step 4: Create prometheus-rule.yaml**

The `kape_subject` label is mandatory — the alertmanager adapter uses it as the NATS subject.

Create `examples/sre-alertmanager/01-mock-api/prometheus-rule.yaml`:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: mock-api-rules
  namespace: kape-examples
  labels:
    app: mock-api
    release: signoz
spec:
  groups:
    - name: mock-api.rules
      interval: 30s
      rules:
        - alert: MockApiHighErrorRate
          expr: |
            (
              rate(mock_api_requests_total{status="error"}[1m])
              /
              rate(mock_api_requests_total[1m])
            ) > 0.1
          for: 1m
          labels:
            severity: warning
            kape_subject: kape.events.alertmanager.mock-api-errors
          annotations:
            summary: "High error rate on mock-api"
            description: "mock-api error rate is {{ $value | humanizePercentage }} over the last 1m"
```

- [ ] **Step 5: Create kustomization.yaml**

Create `examples/sre-alertmanager/01-mock-api/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - deployment.yaml
  - service.yaml
  - service-monitor.yaml
  - prometheus-rule.yaml
```

- [ ] **Step 6: Dry-run validate**

```bash
kubectl apply -k examples/sre-alertmanager/01-mock-api/ --dry-run=client 2>&1
```

Expected: all resources validated without errors (ServiceMonitor/PrometheusRule errors are fine if CRDs not installed locally).

- [ ] **Step 7: Commit**

```bash
git add examples/sre-alertmanager/01-mock-api/
git commit -m "feat(examples): add mock-api k8s manifests with PrometheusRule"
```

---

## Task 4: load-generator Kubernetes manifests

**Files:**
- Create: `examples/sre-alertmanager/02-load-generator/deployment.yaml`
- Create: `examples/sre-alertmanager/02-load-generator/kustomization.yaml`

- [ ] **Step 1: Create deployment.yaml**

Uses `curlimages/curl` — no custom image required. The shell loop polls mock-api continuously.

Create `examples/sre-alertmanager/02-load-generator/deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: load-generator
  namespace: kape-examples
  labels:
    app: load-generator
spec:
  replicas: 1
  selector:
    matchLabels:
      app: load-generator
  template:
    metadata:
      labels:
        app: load-generator
    spec:
      containers:
        - name: load-generator
          image: curlimages/curl:8.7.1
          command:
            - /bin/sh
            - -c
            - |
              TARGET=${TARGET_URL:-http://mock-api.kape-examples.svc.cluster.local:8080/api/status}
              INTERVAL_MS=${POLL_INTERVAL_MS:-500}
              echo "{\"msg\":\"load-generator starting\",\"target\":\"$TARGET\",\"interval_ms\":$INTERVAL_MS}"
              while true; do
                STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$TARGET" 2>/dev/null)
                echo "{\"ts\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"status_code\":$STATUS}"
                sleep $(echo "scale=3; $INTERVAL_MS/1000" | bc)
              done
          env:
            - name: TARGET_URL
              value: "http://mock-api.kape-examples.svc.cluster.local:8080/api/status"
            - name: POLL_INTERVAL_MS
              value: "500"
          resources:
            requests:
              cpu: 10m
              memory: 16Mi
            limits:
              cpu: 50m
              memory: 32Mi
```

- [ ] **Step 2: Create kustomization.yaml**

Create `examples/sre-alertmanager/02-load-generator/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - deployment.yaml
```

- [ ] **Step 3: Dry-run validate**

```bash
kubectl apply -k examples/sre-alertmanager/02-load-generator/ --dry-run=client 2>&1
```

Expected: `deployment.apps/load-generator created (dry run)`

- [ ] **Step 4: Commit**

```bash
git add examples/sre-alertmanager/02-load-generator/
git commit -m "feat(examples): add load-generator deployment"
```

---

## Task 5: mock-webhook-receiver Go source

**Files:**
- Create: `examples/sre-alertmanager/src/mock-webhook/receiver.go`
- Create: `examples/sre-alertmanager/src/mock-webhook/receiver_test.go`
- Create: `examples/sre-alertmanager/src/mock-webhook/main.go`
- Create: `examples/sre-alertmanager/src/mock-webhook/Dockerfile`

- [ ] **Step 1: Write the failing test**

Create `examples/sre-alertmanager/src/mock-webhook/receiver_test.go`:

```go
package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReceiver_PostJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	body := `{"severity":"high","root_cause":"db_timeout"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	var buf bytes.Buffer
	h := newReceiver(&buf)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 got %d", rec.Code)
	}
	if !strings.Contains(buf.String(), "db_timeout") {
		t.Errorf("expected log to contain 'db_timeout', got: %s", buf.String())
	}
}

func TestReceiver_NonPostReturns405(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)

	var buf bytes.Buffer
	h := newReceiver(&buf)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 got %d", rec.Code)
	}
}

func TestReceiver_InvalidJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("not-json"))

	var buf bytes.Buffer
	h := newReceiver(&buf)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 even for invalid JSON, got %d", rec.Code)
	}
	if !strings.Contains(buf.String(), "not-json") {
		t.Errorf("expected raw body in log, got: %s", buf.String())
	}
}
```

- [ ] **Step 2: Run test — verify it fails**

```bash
cd examples/sre-alertmanager/src/mock-webhook
go test ./... 2>&1 | head -10
```

Expected: `FAIL` — `newReceiver undefined`

- [ ] **Step 3: Initialize go module**

```bash
cd examples/sre-alertmanager/src/mock-webhook
go mod init github.com/kape-io/kape/examples/mock-webhook
go mod tidy
```

- [ ] **Step 4: Write receiver.go**

Create `examples/sre-alertmanager/src/mock-webhook/receiver.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type receiver struct {
	out io.Writer
}

func newReceiver(out io.Writer) *receiver {
	return &receiver{out: out}
}

func (rec *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ts := time.Now().UTC().Format(time.RFC3339)

	var pretty interface{}
	if err := json.Unmarshal(body, &pretty); err == nil {
		out, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Fprintf(rec.out, "[%s] KAPE decision received:\n%s\n---\n", ts, out)
	} else {
		fmt.Fprintf(rec.out, "[%s] KAPE payload (raw): %s\n---\n", ts, body)
	}

	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 5: Write main.go**

Create `examples/sre-alertmanager/src/mock-webhook/main.go`:

```go
package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	port := envOr("PORT", "9000")

	rec := newReceiver(os.Stdout)
	mux := http.NewServeMux()
	mux.Handle("/webhook", rec)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	fmt.Printf(`{"msg":"mock-webhook-receiver starting","port":%q}`+"\n", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		fmt.Fprintf(os.Stderr, `{"msg":"server error","error":%q}`+"\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 6: Run tests — verify they pass**

```bash
cd examples/sre-alertmanager/src/mock-webhook
go test ./... -v 2>&1
```

Expected:
```
--- PASS: TestReceiver_PostJSON (0.00s)
--- PASS: TestReceiver_NonPostReturns405 (0.00s)
--- PASS: TestReceiver_InvalidJSON (0.00s)
PASS
```

- [ ] **Step 7: Write Dockerfile**

Create `examples/sre-alertmanager/src/mock-webhook/Dockerfile`:

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mock-webhook .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/mock-webhook /mock-webhook
ENTRYPOINT ["/mock-webhook"]
```

- [ ] **Step 8: Commit**

```bash
git add examples/sre-alertmanager/src/mock-webhook/
git commit -m "feat(examples): add mock-webhook-receiver Go source with tests"
```

---

## Task 6: mock-webhook-receiver Kubernetes manifests

**Files:**
- Create: `examples/sre-alertmanager/04-mock-webhook/deployment.yaml`
- Create: `examples/sre-alertmanager/04-mock-webhook/service.yaml`
- Create: `examples/sre-alertmanager/04-mock-webhook/kustomization.yaml`

- [ ] **Step 1: Create deployment.yaml**

Create `examples/sre-alertmanager/04-mock-webhook/deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mock-webhook-receiver
  namespace: kape-examples
  labels:
    app: mock-webhook-receiver
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mock-webhook-receiver
  template:
    metadata:
      labels:
        app: mock-webhook-receiver
    spec:
      containers:
        - name: mock-webhook-receiver
          image: ghcr.io/kape-io/kape-mock-webhook:latest
          imagePullPolicy: IfNotPresent
          ports:
            - name: http
              containerPort: 9000
          env:
            - name: PORT
              value: "9000"
          livenessProbe:
            httpGet:
              path: /healthz
              port: 9000
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /healthz
              port: 9000
            initialDelaySeconds: 3
            periodSeconds: 5
          resources:
            requests:
              cpu: 10m
              memory: 16Mi
            limits:
              cpu: 50m
              memory: 32Mi
```

- [ ] **Step 2: Create service.yaml**

Create `examples/sre-alertmanager/04-mock-webhook/service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: mock-webhook-receiver
  namespace: kape-examples
  labels:
    app: mock-webhook-receiver
spec:
  selector:
    app: mock-webhook-receiver
  ports:
    - name: http
      port: 80
      targetPort: 9000
```

- [ ] **Step 3: Create kustomization.yaml**

Create `examples/sre-alertmanager/04-mock-webhook/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - deployment.yaml
  - service.yaml
```

- [ ] **Step 4: Dry-run validate**

```bash
kubectl apply -k examples/sre-alertmanager/04-mock-webhook/ --dry-run=client 2>&1
```

Expected:
```
deployment.apps/mock-webhook-receiver created (dry run)
service/mock-webhook-receiver created (dry run)
```

- [ ] **Step 5: Commit**

```bash
git add examples/sre-alertmanager/04-mock-webhook/
git commit -m "feat(examples): add mock-webhook-receiver k8s manifests"
```

---

## Task 7: KAPE resources

**Files:**
- Create: `examples/sre-alertmanager/03-kape/kape-schema.yaml`
- Create: `examples/sre-alertmanager/03-kape/kape-tool-log-reader.yaml`
- Create: `examples/sre-alertmanager/03-kape/kape-handler.yaml`
- Create: `examples/sre-alertmanager/03-kape/kustomization.yaml`

- [ ] **Step 1: Create kape-schema.yaml**

Create `examples/sre-alertmanager/03-kape/kape-schema.yaml`:

```yaml
apiVersion: kape.io/v1alpha1
kind: KapeSchema
metadata:
  name: sre-decision
  namespace: kape-examples
spec:
  version: v1
  jsonSchema:
    type: object
    additionalProperties: false
    required:
      - severity
      - root_cause
      - affected_service
      - recommendation
      - evidence_summary
    properties:
      severity:
        type: string
        enum:
          - low
          - medium
          - high
          - critical
        description: "Assessed severity of the incident"
      root_cause:
        type: string
        description: "Identified root cause based on log evidence"
      affected_service:
        type: string
        description: "Name of the affected service"
      recommendation:
        type: string
        description: "Recommended remediation action"
      evidence_summary:
        type: string
        description: "Summary of evidence found in pod logs"
```

- [ ] **Step 2: Create kape-tool-log-reader.yaml**

The k8s-mcp server at `http://k8s-mcp-svc.kape-system:8080` must be running (installed by KAPE Helm). The allowedTools restrict the agent to read-only log access only.

Create `examples/sre-alertmanager/03-kape/kape-tool-log-reader.yaml`:

```yaml
apiVersion: kape.io/v1alpha1
kind: KapeTool
metadata:
  name: k8s-log-reader
  namespace: kape-examples
spec:
  description: "Read-only access to Kubernetes pod logs in kape-examples namespace"
  mcp:
    upstream:
      url: "http://k8s-mcp-svc.kape-system:8080"
      transport: sse
    allowedTools:
      - list_pods
      - get_pod_logs
```

- [ ] **Step 3: Create kape-handler.yaml**

The handler subscribes to `kape.events.alertmanager.mock-api-errors` — the exact value of the `kape_subject` label in the PrometheusRule. The action POSTs the structured decision to mock-webhook-receiver using CEL `condition: "true"` to fire on every decision.

Create `examples/sre-alertmanager/03-kape/kape-handler.yaml`:

```yaml
apiVersion: kape.io/v1alpha1
kind: KapeHandler
metadata:
  name: sre-mock-api-monitor
  namespace: kape-examples
spec:
  schemaRef: sre-decision
  trigger:
    source: kape.events.alertmanager.mock-api-errors
    type: kape.events.alertmanager.mock-api-errors
    maxEventAgeSeconds: 300
  tools:
    - ref: k8s-log-reader
  llm:
    provider: anthropic
    model: claude-sonnet-4-6
    maxIterations: 10
    systemPrompt: |
      You are an SRE agent monitoring the mock-api service in the kape-examples namespace.

      When you receive an AlertManager alert about high error rates, follow this procedure:

      1. Read the alert metadata: note the alertname, severity label, and description annotation.

      2. List pods in the kape-examples namespace using the k8s-log-reader tool to find mock-api pods.

      3. Fetch the last 50 lines of logs from each mock-api pod. Look for structured JSON log lines
         with fields: status_code, error_reason, latency_ms. Count the frequency of each error_reason
         value (db_timeout, upstream_unavailable, nil_pointer).

      4. Based on the most frequent error_reason, determine:
         - severity: "low" (<20% errors), "medium" (20-40%), "high" (40-70%), "critical" (>70%)
         - root_cause: the dominant error_reason from the logs
         - affected_service: "mock-api"
         - recommendation: one concrete remediation step appropriate to the root_cause
         - evidence_summary: a 1-2 sentence summary of what the logs showed

      Always populate all five fields. Be concise and factual.
  actions:
    - name: notify-sre-webhook
      type: webhook
      condition: "true"
      data:
        url: "http://mock-webhook-receiver.kape-examples.svc.cluster.local/webhook"
        method: POST
        body:
          severity: "{{ decision.severity }}"
          root_cause: "{{ decision.root_cause }}"
          affected_service: "{{ decision.affected_service }}"
          recommendation: "{{ decision.recommendation }}"
          evidence_summary: "{{ decision.evidence_summary }}"
  scaling:
    minReplicas: 1
    maxReplicas: 3
    natsLagThreshold: 5
```

- [ ] **Step 4: Create kustomization.yaml**

Create `examples/sre-alertmanager/03-kape/kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - kape-schema.yaml
  - kape-tool-log-reader.yaml
  - kape-handler.yaml
```

- [ ] **Step 5: Dry-run validate**

```bash
kubectl apply -k examples/sre-alertmanager/03-kape/ --dry-run=client 2>&1
```

Expected: resources created (dry run) — CRD validation errors are expected if KAPE CRDs not installed locally.

- [ ] **Step 6: Commit**

```bash
git add examples/sre-alertmanager/03-kape/
git commit -m "feat(examples): add KAPE resources — KapeSchema, KapeTool, KapeHandler"
```

---

## Task 8: SigNoz setup guide

**Files:**
- Create: `examples/sre-alertmanager/00-signoz/README.md`
- Create: `examples/sre-alertmanager/00-signoz/alertmanager-receiver-patch.yaml`

- [ ] **Step 1: Create alertmanager-receiver-patch.yaml**

This ConfigMap patch adds the KAPE alertmanager adapter as a receiver in SigNoz's AlertManager. The adapter URL assumes the KAPE Helm chart is installed in `kape-system` and the adapter Service is named `kape-adapter-alertmanager` on port `8080`.

Create `examples/sre-alertmanager/00-signoz/alertmanager-receiver-patch.yaml`:

```yaml
# Apply with:
#   kubectl -n platform patch configmap signoz-alertmanager \
#     --patch-file alertmanager-receiver-patch.yaml
#
# This adds the kape-alertmanager-adapter as a webhook receiver.
# Adjust 'signoz-alertmanager' to match the actual ConfigMap name
# in your SigNoz installation (check: kubectl get cm -n platform | grep alertmanager).
apiVersion: v1
kind: ConfigMap
metadata:
  name: signoz-alertmanager
  namespace: platform
data:
  config.yml: |
    global:
      resolve_timeout: 5m

    route:
      group_by: ['alertname', 'kape_subject']
      group_wait: 30s
      group_interval: 5m
      repeat_interval: 1h
      receiver: kape-adapter

    receivers:
      - name: kape-adapter
        webhook_configs:
          - url: 'http://kape-adapter-alertmanager.kape-system.svc.cluster.local:8080/webhook'
            send_resolved: false
```

- [ ] **Step 2: Create README.md**

Create `examples/sre-alertmanager/00-signoz/README.md`:

```markdown
# 00 — SigNoz Setup

SigNoz provides the Prometheus + AlertManager stack for this example.
No k8s manifests are maintained here — only install instructions.

## Prerequisites

- Helm 3.x
- kubectl access to your cluster

## Install SigNoz

```bash
helm repo add signoz https://charts.signoz.io
helm repo update

helm install signoz signoz/signoz \
  --namespace platform \
  --create-namespace \
  --set alertmanager.enabled=true \
  --wait --timeout 10m
```

Verify all pods are running:

```bash
kubectl get pods -n platform
```

## Configure AlertManager to send to KAPE

SigNoz ships with AlertManager. You need to add the KAPE alertmanager adapter as a webhook receiver.

### Option A — patch the ConfigMap directly

```bash
kubectl -n platform apply -f alertmanager-receiver-patch.yaml
```

Then restart AlertManager to pick up the config:

```bash
kubectl -n platform rollout restart deployment/signoz-alertmanager 2>/dev/null \
  || kubectl -n platform rollout restart statefulset/signoz-alertmanager
```

### Option B — via SigNoz UI

1. Open SigNoz UI (port-forward: `kubectl port-forward -n platform svc/signoz-frontend 3301:3301`)
2. Navigate to **Alerts → Alert Channels → New Channel**
3. Type: **Webhook**, URL: `http://kape-adapter-alertmanager.kape-system.svc.cluster.local:8080/webhook`
4. Save and set as default channel

## Configure Prometheus to scrape kape-examples

SigNoz's Prometheus discovers ServiceMonitors via label selector `release: signoz`.
The ServiceMonitor in `01-mock-api/service-monitor.yaml` already carries this label.

Verify discovery after applying the example:

```bash
kubectl port-forward -n platform svc/signoz-prometheus 9090:9090
# Open http://localhost:9090/targets — look for "kape-examples/mock-api"
```

## Verify AlertManager is routing to KAPE

```bash
kubectl port-forward -n platform svc/signoz-alertmanager 9093:9093
# Open http://localhost:9093 — check Receivers tab for "kape-adapter"
```
```

- [ ] **Step 3: Commit**

```bash
git add examples/sre-alertmanager/00-signoz/
git commit -m "feat(examples): add SigNoz setup guide and AlertManager receiver patch"
```

---

## Task 9: Main README + final kustomization

**Files:**
- Create: `examples/sre-alertmanager/README.md`

- [ ] **Step 1: Create README.md**

Create `examples/sre-alertmanager/README.md`:

```markdown
# SRE AlertManager Example

End-to-end KAPE example: a mock API with random failures, a load generator driving traffic,
SigNoz firing AlertManager alerts when error rates spike, and a KapeHandler reasoning over
pod logs to produce a structured SRE decision — posted to a mock webhook receiver you can
inspect with `kubectl logs`.

## Architecture

```
load-generator ──→ mock-api (40% failure rate)
                       │
                    /metrics (Prometheus)
                       │
                    SigNoz PrometheusRule: error_rate > 10% → MockApiHighErrorRate alert
                       │
                    AlertManager → kape-alertmanager-adapter (kape-system)
                       │
                    NATS JetStream: kape.events.alertmanager.mock-api-errors
                       │
                    KapeHandler: sre-mock-api-monitor
                    │  reads mock-api pod logs via k8s-mcp (KapeTool)
                    │  produces KapeSchema-structured SRE decision
                       │
                    mock-webhook-receiver (kubectl logs to see decision)
```

## Prerequisites

| Component | Notes |
|---|---|
| Kubernetes cluster | kind, minikube, or cloud cluster |
| KAPE installed | `helm install kape ./helm -n kape-system` — provides NATS, operator, runtime, k8s-mcp |
| SigNoz installed | See `00-signoz/README.md` for helm install |
| Prometheus CRDs | Installed by SigNoz: `ServiceMonitor`, `PrometheusRule` |
| LLM API key | Secret `kape-anthropic` with key `ANTHROPIC_API_KEY` in `kape-examples` namespace |

### Create the LLM API key secret

```bash
kubectl create namespace kape-examples
kubectl create secret generic kape-anthropic \
  --from-literal=ANTHROPIC_API_KEY=<your-key> \
  -n kape-examples
```

## Quick Start

### Step 1 — Install SigNoz and configure AlertManager

Follow `00-signoz/README.md`.

### Step 2 — Build and load images (kind / minikube)

```bash
# mock-api
docker build -t ghcr.io/kape-io/kape-mock-api:latest src/mock-api/
kind load docker-image ghcr.io/kape-io/kape-mock-api:latest   # or: minikube image load

# mock-webhook
docker build -t ghcr.io/kape-io/kape-mock-webhook:latest src/mock-webhook/
kind load docker-image ghcr.io/kape-io/kape-mock-webhook:latest
```

> If using podman: replace `docker` with `podman` throughout.

### Step 3 — Apply all example resources

```bash
kubectl apply -k examples/sre-alertmanager/
```

### Step 4 — Verify workloads are running

```bash
kubectl get pods -n kape-examples
# NAME                                    READY   STATUS    RESTARTS
# mock-api-xxxxx                          1/1     Running   0
# load-generator-xxxxx                    1/1     Running   0
# mock-webhook-receiver-xxxxx             1/1     Running   0
# sre-mock-api-monitor-xxxxx              2/2     Running   0   ← KapeHandler pod (2 = runtime + kapeproxy)
```

### Step 5 — Watch mock-api logs (evidence stream)

```bash
kubectl logs -l app=mock-api -n kape-examples -f
# {"request_id":"...","status_code":500,"latency_ms":1,"error_reason":"db_timeout","timestamp":"..."}
# {"request_id":"...","status_code":200,"latency_ms":0,"timestamp":"..."}
```

### Step 6 — Wait for the alert to fire (~2 minutes)

With `FAILURE_RATE=0.4`, the error rate exceeds 10% immediately.
AlertManager waits 1 minute (`for: 1m`) before firing.

Check alert status:

```bash
kubectl port-forward -n platform svc/signoz-alertmanager 9093:9093
# Open http://localhost:9093 — MockApiHighErrorRate should appear as Firing
```

### Step 7 — Watch KAPE process the alert

```bash
kubectl logs -l app.kubernetes.io/name=kapehandler,kape.io/handler=sre-mock-api-monitor -n kape-examples -f
```

### Step 8 — Read KAPE's SRE decision

```bash
kubectl logs -l app=mock-webhook-receiver -n kape-examples -f
# [2026-04-19T...] KAPE decision received:
# {
#   "severity": "high",
#   "root_cause": "db_timeout",
#   "affected_service": "mock-api",
#   "recommendation": "Check database connection pool settings and downstream DB latency.",
#   "evidence_summary": "23 of 50 log lines showed error_reason=db_timeout in the past minute."
# }
```

## Cleanup

```bash
kubectl delete -k examples/sre-alertmanager/
```

## Building images with Podman

```bash
podman build -t ghcr.io/kape-io/kape-mock-api:latest src/mock-api/
podman build -t ghcr.io/kape-io/kape-mock-webhook:latest src/mock-webhook/
```

## Adjusting failure rate

Edit `01-mock-api/deployment.yaml`, change `FAILURE_RATE` env var (0.0–1.0), then:

```bash
kubectl apply -k examples/sre-alertmanager/01-mock-api/
```

## Running Go tests

```bash
cd src/mock-api && go test ./... -v
cd src/mock-webhook && go test ./... -v
```
```

- [ ] **Step 2: Dry-run full stack**

```bash
kubectl apply -k examples/sre-alertmanager/ --dry-run=client 2>&1
```

Expected: namespace, all deployments, services, ServiceMonitor, PrometheusRule, KAPE CRDs validated.

- [ ] **Step 3: Commit**

```bash
git add examples/sre-alertmanager/README.md
git commit -m "feat(examples): add sre-alertmanager README runbook"
```

---

## Self-Review Checklist

- [x] **Spec coverage**: flow (mock-api → load-gen → SigNoz → adapter → NATS → KapeHandler → mock-webhook) fully covered across Tasks 1-9
- [x] **kape_subject label**: PrometheusRule carries `kape_subject: kape.events.alertmanager.mock-api-errors` (Task 3 Step 4); KapeHandler trigger matches (Task 7 Step 3)
- [x] **KapeTool type**: correctly uses `spec.mcp` (not `eventPublish`) with allowedTools (Task 7 Step 2)
- [x] **KapeHandler action type**: `webhook` (valid enum per CRD) with `condition: "true"` and correct URL (Task 7 Step 3)
- [x] **KapeSchema**: has `additionalProperties: false` and `required` list with ≥1 entry (Task 7 Step 1)
- [x] **load-generator**: uses `curlimages/curl:8.7.1`, no custom image (Task 4 Step 1)
- [x] **podman note**: README references podman as alternative to docker (Task 9 Step 1)
- [x] **LLM API key**: README documents secret creation step (Task 9 Step 1)
- [x] **Go tests**: both programs have tests written before implementation (Tasks 2, 5)
