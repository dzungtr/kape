# controller v1 (promoted M1 spike)

A standalone Go module (`GOWORK=off`, not in the repo go.work) that talks
gRPC **directly** to the deployed OpenShell gateway, creates a sandbox pod
running the pi coding agent in RPC mode, and drives it end-to-end. Domain
language is **agent** (sandbox agent), never session: REST routes at
`/agents...`.

## pi RPC command shapes (verified)

Verified against the locally installed pi (0.87.1,
`@earendil-works/pi-coding-agent`, `docs/rpc-commands.md`):

- **prompt**: `{"id": "req-1", "type": "prompt", "message": "..."}` — the
  spike's baseline shape is correct; `id` is echoed in the command response.
- **abort**: `{"type": "abort"}` — a bare object, **no `id` field**. Pi
  responds `{"type":"response","command":"abort","success":true}` after the
  session becomes idle. Queued steering/followUp messages continue unless
  `clear_queue` is sent first (not done in v1).

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
/usr/bin/go build -o /tmp/gopath/bin/controller .
```

The protos under `proto/` are copied from `github.com/NVIDIA/OpenShell` at tag
`v0.0.116` (main-branch protos drifted: `WorkspaceSelector` oneofs and
`ExecSandboxRequest.sandbox` do **not** match the deployed server).

## Provider

One provider record must exist before creating agents (the controller
attaches it by name, default `openrouter-spike`):

```sh
openshell provider create --name openrouter-spike --type generic \
  --credential OPENROUTER_API_KEY=dummy
```

## Run

Default listen address is `:8081`; under the nono security sandbox only
dev ports (3000, 5173) are allowed to listen, so pass `-listen :3000`:

```sh
/tmp/gopath/bin/controller -listen :3000   # unsandboxed: omit -listen (defaults to :8081)
```

## Demo

```sh
# 1. create agent (CreateSandbox -> wait READY -> policy -> exec pi)
ID=$(curl -s -X POST localhost:3000/agents | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
curl -s localhost:3000/agents/$ID   # {"id","sandbox","status":"ready",...}

# 2. subscribe to events (SSE: replay + live)
curl -sN localhost:3000/agents/$ID/events > events.txt &

# 3. send a prompt (202 accepted; 409 if a turn is in flight)
curl -X POST localhost:3000/agents/$ID/prompt \
  -H 'Content-Type: application/json' \
  -d '{"message":"Introduce yourself in one sentence and list the tools you have."}'

# 3b. stop the in-flight turn (stream stays open; settle still lands)
curl -X POST localhost:3000/agents/$ID/abort

# 4. watch events.txt — text_delta events stream the answer, ends with agent_settled

# 5. teardown
curl -X DELETE localhost:3000/agents/$ID
kubectl get sandbox -A      # empty
openshell sandbox list      # empty
```

## Architecture sketch

```
curl ──HTTP──▶ m1-controller (localhost:3000/8081)
                │  POST /agents ────────▶ CreateSandbox (pi image, provider, 1cpu/2Gi)
                │                          waitReady (poll phase) → UpdateConfig (net policy)
                │                          status FSM: creating → ready
                │  POST /prompt ────────▶ ExecSandboxInteractive (bidi gRPC, mTLS to :32353)
                │                          stdin ← JSONL {"id","type":"prompt","message"}
                │                          409 if a turn is in flight; status working
                │  POST /abort ─────────▶ stdin ← {"type":"abort"} (no id; verified)
                │  GET  /events (SSE) ◀── stdout → verbatim JSONL pi records → EventHub
                │                          agent_settled record → status ready
                │                          exec break / pi exit / sandbox ERROR → failed
                │                          + agent.failed lifecycle event
                │  DELETE /agents/{id} ─▶ cancel exec ctx + DeleteSandbox; status terminated
                └─ in-memory agent map only; no persistence, no auth
                   gateway client is behind GatewayClient (interface) with a
                   FakeGateway for unit tests of the FSM + prompt policy
```

FSM observability note: `creating` and `terminated` are not observable via
`GET /agents/{id}` — the agent is only registered after it reaches `ready`,
and DELETE removes it (subsequent GETs return 404). Both states are visible
only on the SSE event stream (`agent.started`, `agent.terminated`) during
v1's in-memory, no-DB design (see spec #172 user story 21 for restart
semantics).

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
