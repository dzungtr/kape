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
