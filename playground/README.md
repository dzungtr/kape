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

---

## Tilt / Hot Reload

`Tiltfile` in `playground/` wires Tilt to the existing compose stack so source changes are reflected in running containers without a full rebuild.

### Prerequisites

| Tool | Install |
|---|---|
| tilt | https://docs.tilt.dev/install.html |
| All tools listed above | (same as regular playground) |

### How to run

```bash
cd playground && tilt up
```

Tilt opens a browser UI at `http://localhost:10350` showing live status for every resource.

### What each resource does

| Resource | Type | Hot-reload behaviour |
|---|---|---|
| `nats` | compose | No hot-reload — infra only |
| `postgres` | compose | No hot-reload — infra only |
| `qdrant` | compose | No hot-reload — infra only |
| `stub-mcp` | compose + docker_build | Syncs `playground/stub-mcp/main.py` live; full rebuild if `requirements.txt` or `Dockerfile` changes |
| `task-service` | compose | Image rebuilt normally via compose |
| `task-service-hot-reload` | local_resource | Builds Go binary locally, copies it into the running container, and restarts it on any `*.go` change; bypasses the distroless image's lack of a shell |
| `runtime` | compose + docker_build | Syncs `runtime/src/` live; full rebuild if `pyproject.toml` or `uv.lock` changes |
| `dashboard-dev` | local_resource | Runs `npm run dev` (Vite HMR) directly on the host — the compose dashboard service is disabled when using Tilt |
