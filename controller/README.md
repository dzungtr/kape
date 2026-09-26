# controller v1 — sandbox-agent interface

A standalone Go module (`GOWORK=off`, not in the repo go.work) that exposes
one tool contract — eight verbs for launching and tracking sandbox agents —
over two transports on the same port: REST (with SSE) and MCP (Streamable
HTTP). It talks gRPC directly to the deployed OpenShell gateway, creates a
sandbox pod running the pi coding agent in RPC mode, and drives it
end-to-end. Domain language is **agent** (sandbox agent), never session;
REST routes at `/agents...`.

Parent spec: dzungtr/kape#172 (Controller v1).

## Tool contract

| Tool (MCP + REST twin) | REST surface | Semantics |
|---|---|---|
| `create_agent` | `POST /agents` | validates profile overrides pre-gateway (see below), creates the Sandbox CR with ownership labels, waits READY, applies egress policy, opens the pi exec stream → `ready` |
| `get_agent` | `GET /agents/{id}` | live view when in memory; post-restart CR-derived view; 404 unknown |
| `list_agents` | `GET /agents` | gateway sandboxes carrying the `managed-by=kape-controller` ownership label, joined with live state — survives restart |
| `prompt_agent` | `POST /agents/{id}/prompt` | 202; **409 if a turn is in flight** (single in-flight prompt, no queueing in v1) |
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

History is in-memory only, ring-buffered with a `truncated: true` flag so a
runaway agent cannot OOM the controller.

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

## Restart semantics (documented v1 behavior)

- **In-flight turns die** — the pi process lives inside one long-lived
  exec gRPC stream; controller restart cancels it.
- **The registry survives** — `list_agents`/`get_agent` fall back to
  gateway Sandbox CRs carrying the ownership labels (post-restart views are
  CR-derived; reattach to events works).
- **Event history does not survive** — the buffer and the seq space reset.

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

No auth in v1: the tailnet (Tailscale ingress) is the boundary. Container
image build and deployment manifests live in `dzungtr/homelab`, not here.

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
  `id` field**; queued steering messages continue unless `clear_queue` is
  sent first (not done in v1).

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
