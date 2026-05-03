# KAPE Local Dev Playground

Run the full KAPE stack locally — no Kubernetes cluster required.

## Prerequisites

| Tool | Install |
|---|---|
| podman + podman-compose | https://podman.io |
| Go 1.25+ | https://go.dev |
| nats CLI | https://github.com/nats-io/natscli#installation |
| setup-envtest | `go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest` |
| kubectl | https://kubernetes.io/docs/tasks/tools/ |

## Quick start

### 1. Configure

```bash
cp playground/.env.example playground/.env
# Edit playground/.env — set ANTHROPIC_API_KEY

cp playground/runtime/settings.toml.example playground/runtime/settings.toml
# Edit playground/runtime/settings.toml if you want a different handler config
```

### 2. Start the stack

```bash
make playground-up
make playground-logs   # tail logs in a second terminal
```

Services running after `playground-up`:

| Service | URL |
|---|---|
| NATS | `nats://localhost:4222` |
| task-service | `http://localhost:8080` |
| dashboard | `http://localhost:3000` |
| stub-mcp | `http://localhost:8090` |

---

## UC2 — Fire an event through the runtime

```bash
nats pub kape.events.alertmanager.MockApiHighErrorRate \
  --stdin < playground/events/alertmanager-example.json \
  --server nats://localhost:4222
```

Watch the runtime reason through it in `make playground-logs`, then open `http://localhost:3000` to see the task.

See `playground/events/README.md` for all event types.

---

## UC1 — Test the operator with a local API server

```bash
# Install envtest binaries (one-time setup)
export KUBEBUILDER_ASSETS=$(setup-envtest use 1.32.0 --bin-dir /tmp/envtest-bins -p path)

# Terminal 1: start the compose stack (provides NATS, postgres, etc.)
make playground-up

# Terminal 2: start the operator against a local API server
make playground-operator
# → writes kubeconfig.playground to the repo root, then blocks

# Terminal 3: apply a handler and inspect the results
kubectl --kubeconfig kubeconfig.playground apply -f examples/handlers/karpenter-reconciliation.yaml
kubectl --kubeconfig kubeconfig.playground get kapehandlers,deployments,configmaps -A
```

The operator reconciles the `KapeHandler` CRD into a `Deployment` + `ConfigMap` (containing `settings.toml`) in the local envtest API server.

---

## UC3 — Test an adapter publishing to NATS

```bash
# Terminal 1: start infra (only NATS is needed)
make playground-up

# Terminal 2: watch all NATS events
nats sub 'kape.events.>' --server nats://localhost:4222

# Terminal 3: fire a sample alertmanager event
make fire-adapter ADAPTER=alertmanager
# → publishes one CloudEvent to kape.events.alertmanager.MockApiHighErrorRate
```

---

## Tear down

```bash
make playground-down   # stops and removes all containers and volumes
```
