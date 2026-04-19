# SRE AlertManager Example — Design Spec

**Date:** 2026-04-19
**Status:** Approved
**Author:** Dzung Tran

---

## Overview

A fully deployable Kubernetes example demonstrating the end-to-end KAPE pipeline for SRE event handling. A mock API serves requests with random failures, a load generator drives continuous traffic, SigNoz observes the error rate and fires AlertManager alerts, the KAPE alertmanager adapter ingests them into NATS, and a KapeHandler reasons over the alert using pod logs as evidence before posting a structured SRE decision to a mock webhook receiver.

---

## Event Flow

```
load-generator Deployment
    │  HTTP GET /api/status (continuous, configurable rate)
    ▼
mock-api Deployment
    │  random 200 / 500 responses (configurable FAILURE_RATE)
    │  structured JSON logs per request
    │  /metrics endpoint (Prometheus format)
    ▼
SigNoz (Helm) — Prometheus scrapes mock-api via ServiceMonitor
    │  PrometheusRule: error_rate > 10% for 1m → alert fires
    ▼
AlertManager → HTTP POST → kape-alertmanager-adapter (existing)
    ▼
NATS JetStream (kape.events.alertmanager)
    ▼
KapeHandler Pod
    │  LangGraph agent reasons over alert
    │  KapeTool (k8s-log-reader MCP) reads mock-api pod logs as evidence
    │  KapeSchema produces structured SRE decision
    ▼
mock-webhook-receiver Deployment  ← KapeHandler HTTP action posts here
    │  logs the full KAPE decision payload (visible via kubectl logs)
```

---

## Components

### mock-api
- Language: Go, single binary
- Endpoints:
  - `GET /api/status` — returns 200 or 500 based on `FAILURE_RATE` env var (default: `0.3`)
  - `GET /metrics` — Prometheus metrics: `mock_api_requests_total{status}`, `mock_api_latency_seconds`
- Logs a structured JSON line per request: `request_id`, `status_code`, `latency_ms`, `error_reason` (one of: `db_timeout`, `upstream_unavailable`, `nil_pointer`)
- Deployed as a Kubernetes Deployment with a ClusterIP Service

### load-generator
- Shell loop (`curl` in a Deployment) continuously polling `mock-api:8080/api/status`
- Configurable via env var `POLL_INTERVAL_MS` (default: `500`)
- Logs each response code to stdout

### SigNoz (00-signoz)
- Installed via official SigNoz Helm chart (`signoz/signoz`) — no manifests maintained
- `00-signoz/README.md` contains the exact `helm install` command and namespace
- ServiceMonitor and PrometheusRule in `01-mock-api/` hook into SigNoz's Prometheus

### PrometheusRule
- Alert name: `MockApiHighErrorRate`
- Expression: `rate(mock_api_requests_total{status="error"}[1m]) / rate(mock_api_requests_total[1m]) > 0.1`
- For: `1m`
- Fires to AlertManager which POSTs to the existing `kape-alertmanager-adapter` webhook

### KapeSchema
- Fields: `severity` (enum: low/medium/high/critical), `root_cause` (string), `affected_service` (string), `recommendation` (string), `evidence_summary` (string)

### KapeTool — log-reader
- Type: `mcp`
- MCP server: existing `k8s-mcp` at `http://k8s-mcp-svc.kape-system:8080`
- Allowlist: `k8s-mcp-read__get_pod_logs`, `k8s-mcp-read__list_pods`
- Allows the handler agent to fetch recent logs from `mock-api` pods as investigation evidence

### KapeHandler
- Subscribes to: `kape.events.alertmanager`
- System prompt instructs agent to:
  1. Read alert metadata (service, severity, labels)
  2. List mock-api pods via k8s-mcp-read, fetch their recent logs
  3. Identify error patterns in logs
  4. Produce a KapeSchema-compliant SRE decision
- `spec.actions[]`: one HTTP POST action (condition: always) that posts the structured decision to `http://mock-webhook-receiver.kape-examples.svc.cluster.local/webhook`

### mock-webhook-receiver
- Language: Go, single binary
- `POST /webhook` — accepts JSON body, pretty-prints to stdout
- `GET /healthz` — health check
- Deployed as a Deployment + ClusterIP Service
- Developer reads KAPE's decisions via `kubectl logs`

---

## Directory Structure

```
examples/sre-alertmanager/
├── README.md
├── kustomization.yaml
├── 00-signoz/
│   └── README.md
├── 01-mock-api/
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── service-monitor.yaml
│   ├── prometheus-rule.yaml
│   └── kustomization.yaml
├── 02-load-generator/
│   ├── deployment.yaml
│   └── kustomization.yaml
├── 03-kape/
│   ├── kape-schema.yaml
│   ├── kape-tool-log-reader.yaml
│   ├── kape-handler.yaml
│   └── kustomization.yaml
├── 04-mock-webhook/
│   ├── deployment.yaml
│   ├── service.yaml
│   └── kustomization.yaml
└── src/
    ├── mock-api/
    │   ├── main.go
    │   └── Dockerfile
    └── mock-webhook/
        ├── main.go
        └── Dockerfile
```

---

## Namespace

All example resources deploy to `kape-examples` namespace. KAPE system resources remain in `kape-system`.

---

## Acceptance Criteria

1. `kubectl apply -k examples/sre-alertmanager/` deploys all workloads cleanly
2. `kubectl logs -l app=mock-api -n kape-examples` shows structured JSON request logs
3. `kubectl logs -l app=load-generator -n kape-examples` shows continuous polling with mixed 200/500 responses
4. Within ~2 minutes, AlertManager fires `MockApiHighErrorRate` to the alertmanager adapter
5. KapeHandler pod processes the event (visible in handler logs / task-service)
6. `kubectl logs -l app=mock-webhook-receiver -n kape-examples` shows the KAPE SRE decision JSON

---

## Out of Scope

- Real PagerDuty / Slack integration (mock webhook only)
- Persistent storage for mock-api (stateless by design)
- TLS between components (example simplicity)
- RBAC for log-reader MCP (follow existing KapeTool RBAC patterns)
