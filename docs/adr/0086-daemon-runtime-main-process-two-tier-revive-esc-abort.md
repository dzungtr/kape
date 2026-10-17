# Daemon runtime: canonical main process, three stores, two-tier revive, Esc abort

## Status

accepted

## Context

M2 Epic 3 (#192, spec #245) turned the Epic 2 daemon stub (#210 — fake pi, login-shell profile-hook startup, silent pi respawn) into the real runtime. ADR-0083 locked the contract and left the runtime open: who starts the daemon, where pi's session state lives, what owns pi's lifetime, and what revive and abort mean mechanically. Epic 2 also noted the session FSM lacks a `running → paused` edge. Pi Durable (`@earendil-works/pi-durable`) was on the table as an embedded engine (ADR-0083 D9).

## Decision

**Engine: thin pi-subprocess relay.** The daemon stays a Go binary owning `pi --mode rpc` over stdin/stdout. Pi Durable (1.3.0, 2026-10-15) was evaluated and rejected: experimental ("API changes without notice", breaking changes in 1.1.0 and 1.2.0), and pi CLI 1.3.0 does not embed it — adopting it means rebuilding pi CLI's agent surface (MCP, skills, extensions, settings) on a churning API. Revisit triggers: ≥2 consecutive non-breaking minor releases, or a requirement turn-boundary durability cannot meet.

**Startup: the daemon is the sandbox's canonical main process.** The controller passes `command = [kape-daemon]` in `CreateSandboxRequest` (proto field 12); the OpenShell supervisor launches it directly, no shell, no profile hook. Daemon lifetime == sandbox lifetime: **daemon death collapses into sandbox/pod death** — ADR-0083 D6's third failure class absorbs the second, and the `boot_id` preflight now distinguishes only partition (same `boot_id`) from replacement (new sandbox). Pod restart re-runs the canonical command. The #217 profile-hook pattern is the documented fallback if the k8s driver fails to thread `command` live.

**Three stores, three roles.** The controller event log stays orchestration truth (revive drives from it, ADR-0082). The daemon spool is an unacked-record transport buffer only — ADR-0083 D1's "revive-journal seed" narrows to honest-history drain on death. The pi session tree on the sandbox PVC (`--session-dir`, `--session-id` pinned to the **session**, stable across revives) is the model-context resume state. No new cursor format: resume cursor = (`--session-id`, pi branch head).

**Pi crash ownership (daemon alive): lazy respawn, never auto-reprompt.** Pi is restarted on the next prompt/command with the same `--session-id`; a lost prompt is client intent and re-prompting is the host's call. `pi_exit` on every exit; the reserved `pi_restart` notice activates on respawn-after-unexpected-exit (exit code + backoff state). Rapid exit (<5 s) backs off 1 s→30 s; a stable ≥60 s run resets; the daemon never gives up. On daemon SIGTERM, pi dies first (SIGTERM→SIGKILL), then the daemon exits non-zero.

**Revive: two tiers, budgeted, automatic.** Tier 1 (pi-level) is the lazy respawn — no FSM event, no budget. Tier 2 (sandbox-level): on death detection the session FSM takes the new **`running → paused`** edge; the controller replaces the sandbox on the same PVC + template; `daemon.boot` + the first successful pull drive `paused → running`. Budget `REVIVE_MAX=3` per session lifetime, `REVIVE_COOLDOWN=60s`, env-configurable. Exhaustion leaves the session `paused` with agent liveness `failed`, surfaced via `wait_agent`; a front-door re-arm verb resets the budget, or the human Deletes (`paused → terminated`). Commands during `paused` are rejected fail-fast. There is no auto-reinject: accepted-but-undelivered commands are derivable from the log; hosts re-send. The crash window (ADR/#159: one in-flight turn) extends to **one in-flight turn + pi's in-memory queue**.

**Abort = Esc semantics.** Front-door `abort` maps to pi `clear_queue` + `abort`, in that order — pi's `abort` alone continues queued messages (verified against pi rpc-commands). Drained queue texts are spooled as a daemon notice. `clear_queue` stays its own ops/break-glass command (ADR-0083 D4).

**Ops surface stays minimal.** Extended `/status` (`spool_depth`, `pi_restarts`, `backoff`), daemon stdout via supervisor streaming, break-glass SSH exec. No Prometheus endpoint, no admin-mutation verbs in M2 (#187 owns fleet monitoring). Daemon image upgrades ride the Tier-2 revive path and consume budget.

## Consequences

- A dangling branch head (crash mid-turn leaves an unsettled session entry) is the one open mechanics risk: a prototype spike (#246) verifies pi's load behavior; the fallback is a daemon-side session-trimmer rolling the JSONL back to the last settled boundary.
- D6's partition-vs-death vocabulary simplifies: `boot_id` now only ever means partition vs replacement; "daemon alive, pi dead" is the only in-place recovery path.
- The `running ⇄ paused` edge lands in the transition table; `paused` rejection semantics become front-door contract (and dashboard vocabulary, M3).
- Engine swap to Pi Durable, if the revisit triggers fire, costs the controller nothing — the contract stays engine-agnostic (ADR-0083 D9).
- Spec: #245. Tickets: #246–#252 (+ conditional session-trimmer from the #246 verdict).
