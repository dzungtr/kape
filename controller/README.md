# controller v1 — sandbox-agent interface

A standalone Go module (`GOWORK=off`, not in the repo go.work) that exposes
one tool contract — eight verbs for launching and tracking sandbox agents —
over two transports on the same port: REST (with SSE) and MCP (Streamable
HTTP). It talks gRPC directly to the deployed OpenShell gateway, creates a
sandbox pod running the pi coding agent in RPC mode, and drives it
end-to-end. Domain language is **agent** (sandbox agent), never session;
REST routes at `/agents...`.

Data plane (issue #212, ADR-0083): the controller **pulls** pi records from
the in-sandbox daemon (`GET /records?after=<ack cursor>&wait=25s`) and is the
seq authority — the daemon spool never assigns client-facing seqs. The ack
cursor is a store-side origin watermark (the highest durably appended
`spool_id`), so controller restarts re-pull with no gaps and no duplicates.
Commands ride `POST /commands` (202 on acceptance). The gateway exec-stream
pump is demoted to break-glass ops (`-transport exec`).

Parent spec: dzungtr/kape#172 (Controller v1).

## Tool contract

| Tool (MCP + REST twin) | REST surface | Semantics |
|---|---|---|
| `create_agent` | `POST /agents` | validates profile overrides pre-gateway (see below), creates the Sandbox CR with ownership labels, waits READY, applies egress policy, opens the pi exec stream → `ready` |
| `get_agent` | `GET /agents/{id}` | live view when in memory; post-restart CR-derived view; 404 unknown |
| `list_agents` | `GET /agents` | gateway sandboxes carrying the `managed-by=kape-controller` ownership label, joined with live state — survives restart |
| `prompt_agent` | `POST /agents/{id}/prompt` | 202; **409 if a turn is in flight** (single in-flight prompt, no queueing in v1) |
| `steer_agent` | `POST /agents/{id}/steer` | mid-turn steering: injects a message into the in-flight turn (202); **409 when idle** — no turn to steer (issue #215). `clear_queue` stays daemon-leg ops vocabulary only, never a front-door tool |
| `abort` | `POST /agents/{id}/abort` | stops the in-flight turn; agent returns to `ready` |
| `stream_events` | `GET /agents/{id}/events` (SSE) | replayed history then live; `Last-Event-ID`/`after` re-attach. Over MCP this returns the buffered replay as one batch (tool results cannot stream); live push is REST-only |
| `read_events` | `GET /agents/{id}/events?after=…` | cursor pull (below) |
| `delete_agent` | `DELETE /agents/{id}` | cancels exec context, deletes the Sandbox CR → `terminated`; 204, then 404 |

Both surfaces are thin wrappers over the same handler core; error meanings
match exactly (validation failures name the field; unknown agent and
turn-in-flight mirror REST 404/409). A contract-equality test asserts the
MCP tool list equals the REST verb set. `last=N` is a REST-only convenience
for human glances — never an MCP tool. Both params given → 400.

### create_agent profile overrides

All optional; omitted fields fall back to config defaults:

```json
{"name":"my-agent","image":"...","resources":{"cpu":"500m","memory":"1Gi"},
 "provider":"openrouter-spike","model":"z-ai/glm-5.2"}
```

- `name`: lowercase DNS label, **≤19 chars** (OpenShell gateway sandbox-name
  cap, verified live for #179); default `sbx-<agent-id-suffix>`.
- `resources`: Kubernetes CPU/memory quantities (e.g. `500m`, `2Gi`).
- `model`: patched into pi's `models.json` and passed as `PI_MODEL`.
- Validation happens **before** any gateway call — a rejected create never
  leaks a Sandbox CR.

## Status model

Derived purely from controller-observed facts:

```
creating → ready → working → (ready | failed | terminated)
```

- `creating`: Sandbox CR created, waiting READY phase.
- `ready`: exec stream open, no turn in flight (also the stable state after
  a turn settles or an abort completes).
- `working`: prompt in flight.
- `failed`: pi non-zero exit, exec stream break, or sandbox ERROR phase —
  plus an `agent.failed` lifecycle event.
- `terminated`: deleted.

Observability note: `creating` and `terminated` are not observable via
`GET /agents/{id}` — the agent registers only after reaching `ready`, and
DELETE removes it (subsequent GETs return 404). Both states are visible on
the SSE event stream (`agent.started`, `agent.terminated`).

## Event model

Two layers, every event stamped with a monotonically increasing per-agent
`seq`:

- **pi RPC records, verbatim** — the controller never re-wraps valid pi
  JSONL records (`message_update` with `text_delta`/`thinking_delta`
  assistantMessageEvents, `agent_start`/`agent_end`, `turn_start`/
  `turn_end`, `agent_settled`, `response`, `extension_ui_request`, ...).
- **Controller lifecycle events** — `agent.provisioned`, `agent.started`,
  `agent.failed`, `agent.terminated`, each carrying
  `{agent_id, status, seq}`.
- **Wrapped raw lines** — pi occasionally emits non-JSON stdout (node
  warnings, control sequences). These are published wrapped as
  `{"type":"raw","data":"..."}` so the buffer stays JSON-safe (found by the
  #179 live smoke: one bad line made every `read_events` batch fail to
  encode). Controller stderr from the exec wrapper is likewise wrapped as
  `{"type":"stderr",...}`.

History is durable: every event is appended to a single-file embedded store
(bbolt) on a controller-local volume (`KAPE_EVENT_STORE` / `-event-store`;
issue #195), one bucket per agent keyed by big-endian seq. The v1 in-memory
ring and its `truncated: true` flag are gone — the `truncated` response
field remains in the read_events contract but is always false. Event reads
(replay-after-cursor, tail pulls, SSE re-attach) all read the store; the
per-agent seq continues from the log's highest key after a restart, with no
gaps and no reuse. The controller is the single writer of the store; each
append is one committed transaction, so replay after an unclean shutdown
sees exactly the committed tail.

### read_events params

`GET /agents/{id}/events?after=N&limit=M&types=t1,t2`

- **Cursor mode (primary):** `after=N&limit=M` — bounded batches, no gaps or
  duplicates across polls.
- **Type filter:** `types=` a comma-separated list — e.g. lifecycle events
  only (`agent.provisioned,agent.started`). The MCP `read_events` tool
  **excludes `text_delta` records by default** (`include_deltas` opt-in) so
  host-agent context grows ∝ turn count, not token count.
- **`last=N`:** REST-only convenience; both `after` and `last` → 400.
- Slow-subscriber drop policy applies only to the live SSE push channel;
  pulls always read the buffer.

## Restart semantics (documented v2 behavior)

- **In-flight turns die** — the pi process lives inside one long-lived
  exec gRPC stream; controller restart cancels it.
- **The registry survives** — `list_agents`/`get_agent` fall back to
  gateway Sandbox CRs carrying the ownership labels (post-restart views are
  CR-derived; reattach to events works).
- **Event history and cursors survive** — the durable log is persisted and
  new events continue the per-agent seq with no gap or reuse (issue #195).
  Surface-level replay (`read_events?after=<cursor>`, SSE re-attach) does
  **not** yet survive a restart: pre-restart agents are unknown to the
  registry until #197 wires registry→hub rehydration, at which point the
  replayed history becomes reachable through those tools.
- **Deployment durability caveat** — the default store path
  (`controller-events.db`) is relative, so it is container-ephemeral until a
  controller-local volume/PVC is mounted; and the store takes a single-writer
  file lock, so the controller must run with `replicas=1`.

## Config reference

Resolution: compiled default → `KAPE_*` env → flag (flag wins).

| Setting | Env | Flag | Default |
|---|---|---|---|
| gateway address | `KAPE_GATEWAY_ADDR` | `-gateway` | `127.0.0.1:32353` |
| mTLS creds dir | `KAPE_CREDS_DIR` | `-creds` | `~/.config/openshell/gateways/k8s/mtls` |
| listen address | `KAPE_LISTEN_ADDR` | `-listen` | `:8081` |
| default provider | `KAPE_DEFAULT_PROVIDER` | `-provider` | `openrouter-spike` |
| default image | `KAPE_DEFAULT_IMAGE` | `-image` | `ghcr.io/dzungtr/pi-openshell:openrouter` |
| default resources | `KAPE_DEFAULT_RESOURCES` (JSON) | `-resources` | `{"cpu":"1","memory":"2Gi"}` |
| model-gateway URL | `KAPE_MODEL_GATEWAY_URL` | `-model-gateway-url` | `http://model-gateway-http.aperture.svc.cluster.local/v1` |
| event store file | `KAPE_EVENT_STORE` | `-event-store` | `controller-events.db` |
| daemon port (fixed) | `KAPE_DAEMON_PORT` | `-daemon-port` | `9090` |
| sandbox namespace | `KAPE_SANDBOX_NAMESPACE` | `-sandbox-namespace` | `openshell` |
| controller service host | `KAPE_CONTROLLER_SERVICE_HOST` | `-controller-service-host` | `kape-controller.openshell.svc.cluster.local` |
| controller service port | `KAPE_CONTROLLER_SERVICE_PORT` | `-controller-service-port` | `8081` |

## Daemon addressing (issue #214, ADR-0083 D7)

Under the pull transport the controller resolves the in-sandbox daemon
address from the agent-sandbox Sandbox CR on **every reconnect** — there is no
static daemon address in config (podIPs churn when the pod restarts):

1. `Sandbox.status.podIPs[0]` — primary; the direct pod path bypasses the router.
2. Headless-Service DNS — fallback when the pod IP is not (yet) reported;
   `Sandbox.status.serviceFQDN` if set, else `{sandbox}.{ns}.svc.cluster.local`.
   Requires `spec.service: true` on the reconciled SandboxTemplate.
3. Fixed port: `KAPE_DAEMON_PORT` (default `9090`).

The k8s client is in-cluster (`rest.InClusterConfig`) and the ServiceAccount
needs only **get/watch on sandboxes** in the sandbox namespace
(`deploy/rbac.yaml`). Sandbox→controller egress is denied by an explicit L7
deny rule (`ApplyControllerEgressDeny`) merged per sandbox — deny rules take
precedence over allows, so the daemon has no listener path back to the
controller even under future broad allow rules.

No auth in v1: the tailnet (Tailscale ingress) is the boundary. The container
image is built from `controller/Dockerfile` in this repo (`make build-images`
→ `kape-controller:dev`); deployment manifests for the local cluster live in
the `dzungtr/homelab` GitOps repo.

## MCP transport

The same eight-tool contract is exposed as MCP tools on **Streamable HTTP**
at `/mcp`, same port and process as REST — one ingress route serves both.
MCP SDK: official **`github.com/modelcontextprotocol/go-sdk` v1.8.0**
(stable 1.x, first-class Streamable HTTP, generated schemas; the documented
fallback `mark3labs/mcp-go` was not needed). Because event `data` payloads
are verbatim pi records of varying shape, event-bearing tools
(`read_events`, `stream_events`) declare `any` outputs while returning the
same structured JSON as REST.

## Setup

Prereqs: microk8s cluster with the OpenShell gateway deployed (v0.0.116),
mTLS creds at `~/.config/openshell/gateways/k8s/mtls/`, `kubectl` working,
and the pi sandbox image `ghcr.io/dzungtr/pi-openshell:openrouter` pullable
via the `ghcr-kape` secret in namespace `openshell`.

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

The protos under `proto/` are copied from `github.com/NVIDIA/OpenShell` at
tag `v0.0.116` (main-branch protos drifted: `WorkspaceSelector` oneofs and
`ExecSandboxRequest.sandbox` do **not** match the deployed server).

## Provider

One provider record must exist before creating agents (the controller
attaches it by name, default `openrouter-spike`):

```sh
openshell provider create --name openrouter-spike --type generic \
  --credential OPENROUTER_API_KEY=dummy
```

## Run

Default listen address is `:8081`; under the nono security sandbox only dev
ports (3000, 5173) are allowed to listen, so pass `-listen :3000`:

```sh
/tmp/gopath/bin/controller -listen :3000   # unsandboxed: omit -listen
```

## Smoke (live cluster)

`./smoke.sh` drives the full v1 flow against the live k8s gateway and is
the executable v1 contract check: build → run on `:3000` → create agent
(default profile + one overridden) → SSE subscribe → prompt round trip in
**both** SSE and pull modes → cursor pull incl. type filter → abort →
list → delete, asserting a Sandbox CR exists during and **zero** after.

Teardown is guaranteed: an EXIT trap deletes every created agent, deletes
any leftover CRs, and fails the run unless `kubectl get sandbox -A` is
empty (the preflight also refuses to start on leftover CRs).

```sh
cd controller && ./smoke.sh   # MODEL/LISTEN/BASE env overrides available
```

## Spike findings feeding this design (gotchas)

- **Proto drift**: pin protos to the gateway version (v0.0.116 ≠ main).
- **exec takes the sandbox UUID, not the name** — the spike hit this; the
  controller resolves `SandboxId` from the create response.
- **Egress policy**: sandboxes route all HTTP through an OPA policy proxy
  (`10.200.0.1:3128`); even allowed hosts need an explicit
  `network_policies` rule whose `binaries` include the calling executable.
  The controller applies this via `UpdateConfig` merge ops after create.
- **Provider credentials**: the gateway *withholds* static credentials from
  the sandbox ("unbound" — no endpoint binding in the provider profile),
  `env_count:0`. The model gateway is currently unauthenticated, so the
  controller injects `OPENROUTER_API_KEY=dummy` via the exec environment —
  the env-injection workaround for withheld provider static creds.
- **`PI_CODING_AGENT_DIR` is main-process-only env**; the exec wrapper must
  fall back to `/sandbox/.pi/agent`.
- **Listen ports**: the nono sandbox allows only dev ports (3000/5173); the
  controller takes `-listen` (default `:8081`).
- **pi RPC shapes (verified against pi 0.87.1 `docs/rpc-commands.md`)**:
  prompt `{"id","type":"prompt","message"}` (baseline shape correct; `id`
  echoed in the response); abort `{"type":"abort"}` — a bare object, **no
  `id` field**; steer follows the prompt shape with `type:"steer"
  ({"id","type":"steer","message"}, issue #215); queued steering messages
  continue unless `clear_queue` is sent first (not done in v1).

## Tech debt: working tokens leak into host-agent context

Sandbox-agent **working tokens** (thinking deltas, tool-call records,
message payloads) still flow through the controller's verbatim record
buffer and can reach a host agent's context via SSE or unfiltered pulls —
only the MCP `read_events` default excludes `text_delta`. The next
iteration should enforce the exclusion at the buffer/subscription layer,
not per-transport. Tracked in #164 (context economy) and #154 (event
model); see spec #172 handoffs.

## What M1 validates, and what M2 replaces

M1 validates the **connection-bound exec lifetime** property: the pi
process exists only inside one long-lived `ExecSandboxInteractive` gRPC
stream; the controller holds that stream open, feeds it JSONL, and pumps
stdout into its own fan-out. Everything around it (sandbox create/wait,
network-policy merge, model-gateway reachability, provider quirks) is
per-connection plumbing. M2 replaces exactly this: a **bridge daemon**
in-cluster owning the pi child process and a persistent control channel, so
pi's lifetime decouples from any single controller connection.
