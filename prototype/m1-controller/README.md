# m1-controller (spike)

A local Go binary that talks gRPC **directly** to the deployed OpenShell
gateway, creates a sandbox pod running the pi coding agent in RPC mode, and
drives it end-to-end from localhost. No kapeproxy, no bridge daemon — the
point of M1 is to learn what a controller must own before M2 exists.

## Setup

Prereqs: microk8s cluster with the OpenShell gateway deployed (v0.0.116), mTLS
creds at `~/.config/openshell/gateways/k8s/mtls/`, `kubectl` working, and the
pi sandbox image `ghcr.io/dzungtr/pi-openshell:openrouter` pullable via the
`ghcr-kape` secret in namespace `openshell`.

Toolchain notes (security-sandboxed hosts):

- Invoke go as `/usr/bin/go` (plain `go` may resolve to a denied binary). If
  `~/go` is not writable, set `GOPATH=/tmp/gopath GOCACHE=/tmp/gocache` and
  prepend `$GOPATH/bin` to PATH.
- proto tools are installed with go, then code is generated with buf:

```sh
export GOPATH=/tmp/gopath GOCACHE=/tmp/gocache CGO_ENABLED=0 GOWORK=off
export PATH=$GOPATH/bin:$PATH
/usr/bin/go install github.com/bufbuild/buf/cmd/buf@v1.50.0
/usr/bin/go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6
/usr/bin/go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
buf generate   # regenerates gen/ from proto/ (protos pinned to gateway v0.0.116)
/usr/bin/go build -o /tmp/gopath/bin/m1-controller .
```

The protos under `proto/` are copied from `github.com/NVIDIA/OpenShell` at tag
`v0.0.116` (main-branch protos drifted: `WorkspaceSelector` oneofs and
`ExecSandboxRequest.sandbox` do **not** match the deployed server).

## Provider

One provider record must exist before creating sessions (the controller
attaches it by name, default `openrouter-spike`):

```sh
openshell provider create --name openrouter-spike --type generic \
  --credential OPENROUTER_API_KEY=dummy
```

## Run

Default listen address is `:8081`; under the nono security sandbox only
dev ports (3000, 5173) are allowed to listen, so pass `-listen :3000`:

```sh
/tmp/gopath/bin/m1-controller -listen :3000   # unsandboxed: omit -listen (defaults to :8081)
```

## Demo

```sh
# 1. create session (CreateSandbox -> wait READY -> policy -> exec pi)
ID=$(curl -s -X POST localhost:3000/sessions | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')

# 2. subscribe to events (SSE: replay + live)
curl -sN localhost:3000/sessions/$ID/events > events.txt &

# 3. send a prompt (202 accepted)
curl -X POST localhost:3000/sessions/$ID/prompt \
  -H 'Content-Type: application/json' \
  -d '{"message":"Introduce yourself in one sentence and list the tools you have."}'

# 4. watch events.txt — text_delta events stream the answer, ends with agent_settled

# 5. teardown
curl -X DELETE localhost:3000/sessions/$ID
kubectl get sandbox -A      # empty
openshell sandbox list      # empty
```

## Architecture sketch

```
curl ──HTTP──▶ m1-controller (localhost:3000/8081)
                │  POST /sessions ──────▶ CreateSandbox (pi image, provider, 1cpu/2Gi)
                │                          WaitReady (poll phase) → UpdateConfig (net policy)
                │  POST /prompt ────────▶ ExecSandboxInteractive (bidi gRPC, mTLS to :32353)
                │                          stdin  ← JSONL prompt commands
                │  GET  /events (SSE) ◀── stdout → strict JSONL pi records → EventHub (buffer+fanout)
                │  DELETE /sessions ─────▶ cancel exec ctx + DeleteSandbox
                └─ in-memory session map only; no persistence, no auth
```

Inside the sandbox the exec runs a bash wrapper that patches
`providers.openrouter.baseUrl` in pi's `models.json` to the in-cluster model
gateway (`http://model-gateway-http.aperture.svc.cluster.local/v1` — pi
env-interpolates apiKey/headers but **not** baseUrl), then execs
`pi --mode rpc --no-session`.

## What M1 validates, and what M2 replaces

M1 validates the **connection-bound exec lifetime** property: the pi process
exists only inside one long-lived `ExecSandboxInteractive` gRPC stream; the
controller must hold that stream open, feed it JSONL, and pump stdout into its
own fan-out for SSE. Everything the controller does around it (sandbox create/
wait, network-policy merge, model-gateway reachability, provider quirks) is
per-connection plumbing that a busy controller would have to duplicate per
session. M2 replaces exactly this: a **bridge daemon** running in-cluster next
to (or inside) the sandbox that owns the pi child process and a persistent
control channel, so pi's lifetime decouples from any single controller
connection — sessions survive controller restarts, multiple controllers can
attach, and the JSONL↔SSE plumbing moves from a local Go process into the
platform.

## Spike findings feeding the real controller design

- **Proto drift**: pin protos to the gateway version (v0.0.116 ≠ main).
  Exec RPCs take the sandbox **UUID** (`SandboxId`), not the name.
- **Egress policy**: sandboxes route all HTTP through an OPA policy proxy
  (`10.200.0.1:3128`); even allowed hosts need an explicit
  `network_policies` rule whose `binaries` include the calling executable.
  The controller applies this via `UpdateConfig` merge ops after create.
- **Provider credentials**: the gateway *withholds* static credentials from
  the sandbox ("unbound" — no endpoint binding in the provider profile),
  `env_count:0`. The model gateway is currently unauthenticated, so the
  controller injects `OPENROUTER_API_KEY=dummy` via the exec environment.
  M2 should use a properly bound provider or per-sandbox credential binding.
- **`PI_CODING_AGENT_DIR` is main-process-only env**; the exec wrapper must
  fall back to `/sandbox/.pi/agent`.
- **Listen ports**: the nono sandbox allows only dev ports (3000/5173); the
  controller takes `-listen` (default `:8081`).
