# daemon

In-sandbox passive HTTP server (issue #210, contract ADR-0083): owns the live
pi RPC subprocess, spools unacked records on the sandbox PVC, and serves the
controller's pull transport. It never dials the controller; the poll is the
heartbeat.

## Endpoints (D3)

| Endpoint | Behaviour |
|---|---|
| `GET /healthz` | liveness, unauthenticated |
| `GET /status` | `{agent_id, boot_id, pi_running}` — reconnect preflight |
| `GET /records?after=&wait=` | long-poll spool read, `{spool_id, data}` envelopes, verbatim pi records |
| `POST /commands` | `{command_id, type: prompt\|steer\|abort\|clear_queue, payload}` → 202 + `Location: /commands/{id}` |
| `GET /commands/{id}` | accepted command |

## Configuration (env, flag overrides)

| Env | Default | Meaning |
|---|---|---|
| `KAPE_DAEMON_ADDR` | `127.0.0.1:9090` | listen address (fixed port in-cluster) |
| `KAPE_AGENT_ID` | `unset` | reported by `/status` |
| `KAPE_DAEMON_TOKEN` | — | static bearer placeholder; empty ⇒ all requests 401 (scheme deferred, D5) |
| `KAPE_SPOOL_PATH` | `/var/lib/kape-daemon/spool.jsonl` | PVC spool file |
| `KAPE_SPOOL_MAX_RECORDS` | `10000` | cap; oldest dropped with a `spool_truncated` notice |
| `KAPE_PI_COMMAND` | — | pi argv (comma-separated); empty ⇒ built-in fake pi |

## Records and notices

`data` is the verbatim pi JSONL record (never re-wrapped, `text_delta`
included) or a daemon notice: `pi_stderr`, `pi_exit`, `spool_truncated`.
`pi_restart` is reserved for Epic 3. Spool ids recover from the file tail —
monotonic across daemon restarts; `boot_id` is fresh random per daemon start.

## Fake pi

`/daemon fakepi` runs the built-in scriptable emitter: one JSON command per
stdin line, one prompt ⇒ a deterministic
`turn_started → tool_call → text_delta → agent_settled` turn on stdout, so
downstream slices drive turns deterministically.

## Build & test

```
cd daemon && go test ./...                          # unit tests
podman build -t kape-daemon:dev -f daemon/Dockerfile .
```
