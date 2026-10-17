# daemon

In-sandbox passive HTTP server (issue #210, contract ADR-0083): owns the live
pi RPC subprocess, spools unacked records on the sandbox PVC, and serves the
controller's pull transport. It never dials the controller; the poll is the
heartbeat.

## Endpoints (D3)

| Endpoint | Behaviour |
|---|---|
| `GET /healthz` | liveness, unauthenticated |
| `GET /status` | `{agent_id, boot_id, pi_running, spool_depth, pi_restarts, backoff}` — reconnect preflight + crash-ownership state |
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
| `KAPE_PI_RAPID_EXIT_WINDOW` | `5s` | exit faster than this after start counts as rapid (E4) |
| `KAPE_PI_BACKOFF_INITIAL` | `1s` | backoff before the first rapid-exit respawn (E4) |
| `KAPE_PI_BACKOFF_MAX` | `30s` | respawn backoff cap (E4) |
| `KAPE_PI_STABLE_RUN_RESET` | `60s` | unexpected exit after a run ≥ this resets backoff (E4) |
| `KAPE_PI_SHUTDOWN_GRACE` | `5s` | SIGTERM→SIGKILL grace when killing pi on daemon shutdown (E4) |
| `KAPE_PI_SESSION_ID` | — | session-pinned pi `--session-id` (spec #245 E3, issue #249); threaded by the controller at create; empty ⇒ no session flags (built-in fake pi) |
| `KAPE_PI_SESSION_DIR` | `/sandbox/kape/sessions` | pi `--session-dir` (PVC session tree; used only with a pinned session id) |

## Records and notices

`data` is the verbatim pi JSONL record (never re-wrapped, `text_delta`
included) or a daemon notice: `pi_stderr`, `pi_exit`, `pi_restart`,
`spool_truncated`. Spool ids recover from the file tail — monotonic across
daemon restarts; `boot_id` is fresh random per daemon start.

## Pi crash ownership (spec #245 E4)

Pi is respawned lazily — on the next command, never auto-reprompted — with the
same session. Every unexpected exit (anything the daemon did not initiate)
spools `pi_exit`; every respawn after an unexpected exit spools `pi_restart`
carrying `exit_code`, `backoff_ms` and the `restarts` count. An exit <5 s
after start is rapid and backs the next respawn off exponentially (1 s→30 s
cap); a stable run ≥60 s resets the guard; the daemon never gives up. On
daemon SIGTERM/SIGINT, pi is killed first (SIGTERM→SIGKILL after the grace),
then the daemon exits non-zero (intentional teardown reads via the sandbox
`exit_code`, E2).

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
