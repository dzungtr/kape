# Project Build Friction Report

**Window:** 2026-04-01 → 2026-05-17
**Source signals:** merged PRs (#56–#102), `fix(...)` / `Revert` / fixup commits, Phase 6 D-series additions (D16–D20), Phase 7 closeout audit, recent standups (2026-05-16 / 2026-05-17), slice-7 fixup spec.

This report categorises friction observed while *building* the project — not bugs in shipped behaviour against external users, but cost paid by the team to land each slice. Each category names the failure mode, points at concrete incidents, and (where the data supports it) a one-line trigger to watch for next time.

---

## 1. Under-specified plans → implementer ships the wrong thing faithfully

The highest-leverage category: when a slice plan was ambiguous or wrong, the implementer followed it exactly and the divergence only surfaced at author review.

- **kapeproxy slice 7 `allowedTools` semantics** (PR #57 → fixup PR #98, commits `4479a4c`, `10ae9f1`). The slice-7 plan literally said *"the router reports the names from `allowedTools` when set"*. IMPLEMENTATION-SPEC D14 only defined the `nil` case. The implementer emitted the allowlist verbatim — advertising tools the upstream did not have, and gating `tools/call` without consulting the upstream. Corrected to glob-intersect-with-upstream, deny-by-default (D16, D20).
- **kapeproxy image-tag defaults** (PR #57 → fixup commits `a9c2fe5`, `a9cd003`, `8eb5f1d`). Operator default was hardcoded `"0.7.0"`. IMPLEMENTATION-SPEC §2.1 actually mandates `"latest"` as the in-code default with release pins in `helm/values.yaml`. A mechanical swap from one wrong value (`"stub"`) to another wrong value (`"0.7.0"`) without re-reading the spec.

**Trigger to watch:** any plan section that specifies *one* branch of a conditional (e.g. `nil` case) but not the other. The under-specified branch will be filled by whoever writes the code, and there is a ~50% chance they will be wrong.

---

## 2. Silent scope drops — "done" without integration

Slices were marked complete when unit tests passed, but the wiring step that makes the code reachable from production was never added and never tracked as a follow-up.

- **P7/05 ActionsRouter never invoked** (PR #100, commit `729eecd`). `run_actions()` was fully implemented and unit-tested in #73, but never called from `consumer.py`. The entire post-decision action layer (event-publish, webhook, save_memory) was *dead code at runtime* until the Phase 7 closeout audit caught it.
- **P7/03 `load_skill` never registered** (PR #99, commit `669829c`). `make_load_skill_tool` shipped in `skills.py`; `graph/graph.py` had only a `# TODO(Phase 7.3)` placeholder. The placeholder shipped to main.
- **Slice-7 Tasks 12 + 14 silently dropped** (fixup PR #98 cleanup commits `4293f8b`, `8eb5f1d`). The stub CI workflow and the helm `kapeproxy:` block were in the original slice-7 plan but skipped during implementation; nobody noticed until author review of PR #57.

**Trigger to watch:** an issue closed against "all unit tests pass" without a single test that exercises the new code from its caller's entrypoint. Integration tests at the boundary catch what unit tests structurally cannot.

---

## 3. Generated artifacts diverging from hand-written truth

Codegen tools rewrote files that humans had also edited, silently dropping intent.

- **controller-gen drops CEL XValidation markers** (commits around the CRD regen — `fix(crd): restore CEL validation rules dropped by controller-gen regen`, immediately followed by `Revert "fix(crd)..."` and then `fix(operator): add KapeSchema CEL validation XValidation markers to types file`). The first fix patched the generated CRD; the revert + re-fix moved the source of truth into the Go types file so regen would no longer drop it.
- **OpenAPI / runtime schema drift** (commit `fix(runtime): remove otel_trace_id from UpdateStatusRequest — not in OpenAPI spec`, `fix(task-service): sync gen/api/openapi.yaml with updated source spec`). Runtime sent a field that the contract did not declare; task-service generated YAML lagged its source spec.

**Trigger to watch:** any file with a *"DO NOT EDIT — generated"* header that has hand-edits in `git blame`, or any hand-written file with content the generator does not know it should preserve.

---

## 4. Local-dev tooling parity (Docker ↔ Podman)

The team standardised on Podman; multiple landing pads still assumed Docker.

- **Makefile** (PRs #92, commits `e84a8f6`, `97e06bd`) — replaced docker with podman, then *re-added* `podman-build` after realising downstream still called `docker-build`.
- **Tiltfile** (commits `fix(playground): add podman=True to docker_compose() for Tilt v0.33+`, `fix(playground): make Tiltfile compatible with Podman`).
- **CI auto-push disabled, local kapeproxy build added for playground** (`fix(ci): disable auto-push, add local kapeproxy build for playground`).

**Trigger to watch:** when introducing a new container-using surface (compose file, Tiltfile, Makefile target, CI step), grep the repo for `docker` and `podman` in the same breath and confirm the new surface matches the chosen convention.

---

## 5. Operational defaults that bit us

Defaults that seemed reasonable in isolation produced surprising behaviour in production paths.

- **`allowedTools` empty = "expose all"** flipped to deny-by-default (D20, commit `4479a4c`). The original semantics meant a misconfigured CR would expose every upstream tool to clients. An audited proxy must require explicit opt-in.
- **task-service migration started the HTTP server** (PR #93, commit `011acbb`). `--migrate-only` flag added so the migration container exits after running migrations rather than booting the full server.
- **NATS JetStream dedup required `Nats-Msg-Id` header** (issue #27, `fix(adapters): publish with Nats-Msg-Id so JetStream can dedup`). Dedup silently no-op'd without it.
- **`*bool` vs `bool` for spec fields** (`fix: use *bool for AuditSpec.Enabled to prevent omitempty footgun`, same for `ReplayOnStartup`). `omitempty` plus a zero-value `false` made "explicitly off" indistinguishable from "unset".

**Trigger to watch:** any default that is the *common-case correct* value but the *security-or-data-loss wrong* value when misconfigured. Prefer the safe-default even when it costs ergonomics.

---

## 6. CI / config-language gotchas

Time burned to YAML / shell / bash specifics that have nothing to do with what the code does.

- **Hex color codes coerced to floats** (PR #97, commit `5abf313`). `color: 0e8a16` parsed as a YAML float; required quoting.
- **`bats` not available on Fedora dev box without extra install** (label rollout plan, commit `2166cfb` — `docs(plan): replace bats with pure-bash test runner`). Plan rewritten to use a pure-bash test runner before any of it ran.
- **Bash process substitution required in CI** (`fix(ci): use process substitution in validate-labels.yml`).
- **Lingering `kapeproxy-stub` CI workflow** still triggerable via `workflow_dispatch` after the stub was removed (D19, commit `4293f8b`).

**Trigger to watch:** any config-language value that *looks* like another type (hex, version strings, `yes`/`no`/`on`/`off`, leading zeros). Quote it. Any new test harness — confirm it runs on the dev box before writing the plan around it.

---

## 7. Supply-chain steady-state churn

Background-rate work to keep dependencies clean. Not a one-off cost; a tax.

- `chi` Open Redirect — patched twice in the window: v5.2.2 (CWE-601), then v5.2.4 (CVE-2025-69725).
- Go toolchain bumped to 1.25.10 + `golang.org/x/net` to v0.53.0 for Snyk-flagged CVEs.
- `go-pg` SQL injection patched (CVE-2024-44905).
- Standup datasource label renamed `snyk` → `snyk-finding` (PR #91) so the channel matches the taxonomy.

**Trigger to watch:** this category is steady-state, not avoidable. The investment to reduce it is the `snyk_sbom_scan` PR checklist and the Snyk-finding → GH-issue helper (`2b7ad78`), both of which surface findings before merge instead of in a periodic audit.

---

## 8. Process / spec churn

Decisions made and re-made about how to organise the work itself.

- **Phase 8 spec** went combined → per-issue → renamed-with-issue-numbers in three commits across one day (`e4d1b83`, `eda84b5`, `77f6c8c`), then issue-78 was dropped entirely as *"needs more thinking"* (`75ee9cc`).
- **Label system rollout** required: pure-bash runner replacement, on-demand validator instead of scheduled, `apply-labels` skill replacing a planned PR auto-labeler GHA, retiring `roadmap-sync` after backfill, multiple in-flight plan amendments (commits `500d160`, `e3b6223`, `39fa76f`, `1b088cd`, `2166cfb`).
- **Standup datasource set** added `urgent` + `blocked` after initial rollout (`71d7322`).

**Trigger to watch:** when a "spec PR" attracts more than two follow-up amendment commits in the same branch, the underlying decision is not yet stable — favour landing the spec as *Proposed* and iterating in subsequent PRs over trying to finalise inline.

---

## Cross-cutting observations

- **Audit-after-merge catches what review-before-merge missed.** The Phase 7 closeout audit found dead-code wiring gaps (#99, #100) that neither unit tests nor PR review caught. The slice-7 author-review caught divergences (#98) that the merge could have hidden. Both are now part of the working rhythm and visibly cheaper than discovering the same gaps via on-call incident.
- **Specs that ship without an integration-test acceptance criterion are the dominant source of category 2 friction.** Every Phase 7 closeout finding had a green unit-test suite. The acceptance criterion that would have caught them — "consumer process_message calls run_actions when actions are configured" — was not in the issue.
- **The decision log (Phase 6 D-series, now at D20) is the single most valuable artefact for category 1 friction.** D14 was wrong; D16/D17/D18/D19/D20 supersede it explicitly in-line and the slice-7 fixup spec points at exactly which lines they replace. Future implementers cannot repeat the divergence by accident.
