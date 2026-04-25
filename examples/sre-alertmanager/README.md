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

> **Note:** All commands in this guide must be run from the **repository root** (the directory containing `go.work`), unless otherwise stated.

## Quick Start

### Step 1 — Install SigNoz and configure AlertManager

Follow `00-signoz/README.md`.

### Step 2 — Build and load images (kind / minikube)

```bash
# mock-api
podman build -t ghcr.io/kape-io/kape-mock-api:latest src/mock-api/
kind load docker-image ghcr.io/kape-io/kape-mock-api:latest   # or: minikube image load

# mock-webhook
podman build -t ghcr.io/kape-io/kape-mock-webhook:latest src/mock-webhook/
kind load docker-image ghcr.io/kape-io/kape-mock-webhook:latest
```

> Using docker? Replace `podman` with `docker` throughout.

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
# {"request_id":"...","status_code":500,"latency_ms":5000,"error_reason":"db_timeout","message":"Database connection pool exhausted after 3 retries. Query timed out waiting for available connection.","timestamp":"..."}
# {"request_id":"...","status_code":500,"latency_ms":1200,"error_reason":"upstream_unavailable","message":"Upstream payment service returned HTTP 503. Circuit breaker is OPEN. Last successful call was 42s ago.","timestamp":"..."}
# {"request_id":"...","status_code":200,"latency_ms":1,"message":"Request processed successfully.","timestamp":"..."}
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
# ========================================
#   KAPE SRE DECISION  [2026-04-19T10:32:01Z]
# ========================================
#   severity         : high
#   affected_service : mock-api
#   root_cause       : db_timeout
#   recommendation   : Check database connection pool settings and downstream DB latency.
#   evidence_summary : 23 of 50 log lines showed error_reason=db_timeout in the past minute.
# ========================================
```

## Cleanup

```bash
kubectl delete -k examples/sre-alertmanager/
```

## Adjusting failure rate

Edit `01-mock-api/deployment.yaml`, change `FAILURE_RATE` env var (0.0–1.0), then:

```bash
kubectl apply -k examples/sre-alertmanager/01-mock-api/
```

## Running Go tests

```bash
cd examples/sre-alertmanager/src/mock-api && GOWORK=off go test ./... -v
cd examples/sre-alertmanager/src/mock-webhook && GOWORK=off go test ./... -v
```
