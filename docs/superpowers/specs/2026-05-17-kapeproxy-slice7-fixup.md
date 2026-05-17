# kapeproxy Slice 7 Fixup — Spec

**Date:** 2026-05-17
**Author:** Dzung Tran
**Depends on:** [Phase 6 — Full Operator Design](./2026-04-19-phase6-full-operator-design.md), [`docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md`](../../roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md), [`docs/roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md`](../../roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md)
**Status:** Proposed
**Supersedes:** `slice-7-kapeproxy-binary.md:381` (the wrong allowlist-filter rule) and `docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md:484` (D14 — "expose all" semantics, now D20 deny-by-default). Extends IMPLEMENTATION-SPEC D-series with D16–D20.

---

## 1. Goal

Correct three divergences that slipped through slice 7 ([PR #57](https://github.com/dzungtr/kape/pull/57)) before it merges:

1. **Tools-filter semantics** — `allowedTools` was implemented as a literal list of exact names that get emitted verbatim. The corrected rule is *glob patterns evaluated against the upstream's real tool list* — `tools/list` and `tools/call` must agree on the same exposed set.
2. **Image-tag defaults** — operator and dev tooling hardcoded the release pin `0.7.0`. The corrected defaults are `latest` (operator code), `:local` (playground / Tilt), and a single release pin lives in `helm/values.yaml`.
3. **Two undone slice-7 tasks** — Task 12 (remove the stub CI workflow) and Task 14 (add the `kapeproxy` block to `helm/values.yaml`) were silently dropped. Both are picked up here.

No runtime behaviour beyond these three items changes. JSON-RPC server, redaction, audit logging, OTEL, upstream client, and the operator reconcilers are untouched.

---

## 2. Background

PR #57 (`feat/phase6-slice7-kapeproxy-binary`) shipped the production `kapeproxy` binary per the slice-7 plan. Author review surfaced two classes of divergence from the project's vision plus two carried-over tasks. Each has a different root cause:

### How each divergence happened

**A. Tools filter (plan-level error).** The slice-7 plan ([`slice-7-kapeproxy-binary.md:381`](../../roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md)) literally instructed:

> "For `List()`, the router reports the names from `allowedTools` when set, and asks the upstream's cached `Available()` tool list otherwise."

So the plan was wrong. The IMPLEMENTATION-SPEC ([D14, line 484](../../roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md)) only defined the `nil` case (`"expose all"`). The populated case was under-specified, the plan filled it with the wrong rule, and the implementer faithfully followed the plan. The shipped `kapeproxy/internal/proxy/router.go:81-84` emits allowlist entries verbatim and `router.go:50-55` gates `tools/call` by membership in the allowlist without consulting the upstream — so `tools/list` can advertise tools that don't exist, and `tools/call` can accept calls to tools the upstream cannot serve.

**B. Image tags (mechanical port-forward error).** The slice-5 plan codified `ver = "stub"` as a *transitional* default. Slice 7 was meant to replace it. The implementer instead hardcoded `0.7.0` in operator code (`operator/domain/config/config.go:71`) and in playground/Tilt files. The IMPLEMENTATION-SPEC ([§2.1, line 161](../../roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md)) actually mandates `latest` as the default. The default got mechanically swapped from one wrong value (`stub`) to another wrong value (`0.7.0`) without reading the spec.

**C. Two undone tasks (scope-drop).** Task 12 (remove the slice-5 stub CI workflow at `.github/workflows/kapeproxy-stub.yml`) and Task 14 (add the `kapeproxy` block to `helm/values.yaml`) both appear in the original slice-7 plan but were skipped during implementation. The stub workflow is still in the tree (now only triggerable via `workflow_dispatch`) and `helm/values.yaml` has no `kapeproxy:` block at all.

---

## 3. Corrected Behaviours

### 3.A Tools filter — deny-by-default glob intersection with upstream

**New contract.** `allowedTools` entries are **glob patterns** (shell-style, evaluated with stdlib [`path.Match`](https://pkg.go.dev/path#Match)). The exposed tool set is the *intersection* of upstream's real tool list with the union of glob matches:

```
exposed[upstream] = { t ∈ upstream.ListTools() | ∃ g ∈ allowedTools : path.Match(g, t) }
```

Both `List()` (`tools/list` advertisement) and `Route()` (`tools/call` gating) must compute the same exposed set from this formula. A tool that does not appear in `List()` must be rejected by `Route()`, and a tool that does appear in `List()` must be accepted by `Route()` if the upstream actually has it.

**Deny-by-default.** A `nil`, omitted, or `[]` `allowedTools` is an *empty* set of globs. The union of an empty set of globs matches nothing, so the intersection is empty — the upstream contributes **zero** tools. This is a deliberate flip from the original D14 semantics (which treated empty as "expose all"): an audited proxy must require an explicit opt-in before forwarding traffic to an upstream.

To expose every tool an upstream advertises, the operator writes `allowedTools: ["*"]` explicitly. Because upstream tool names are flat (no `/` path separator), the single-glob `"*"` matches every name returned by `upstream.ListTools()`.

**Worked example.** Upstream advertises `["k8s_get_pods", "k8s_list_namespaces", "helm_install"]`:

| `allowedTools` | `List()` output | `Route("up__k8s_get_pods")` | `Route("up__helm_install")` |
|---|---|---|---|
| `nil` / omitted / `[]` | (none) | rejected (deny-by-default) | rejected (deny-by-default) |
| `["k8s_*"]` | `k8s_get_pods`, `k8s_list_namespaces` | hits upstream | rejected (no glob match) |
| `["nonexistent"]` | (none) | rejected | rejected |
| `["k8s_get_pods", "nonexistent"]` | `k8s_get_pods` only | hits upstream | rejected (no glob match) |
| `["*"]` | all three | hits upstream | hits upstream |

`nonexistent` is silently dropped — it matches no upstream tool, so it cannot be exposed. Exact-name entries (no `*`/`?`/`[`) remain valid because `path.Match("foo", "foo") == true`.

**`tools/call` rejection.** When `Route(namespaced)` returns `(nil, false)` because the deny-by-default rule (or any other check) excluded the tool, the JSON-RPC server responds with MCP error code **`-32601` (method not found)**. This is the same error code already used for genuinely unknown tools — operators cannot distinguish "not in your allowlist" from "doesn't exist upstream" via the wire response, which is the intended security posture (do not leak the upstream's tool catalog to clients without an allowlist entry).

**Files touched.** `kapeproxy/internal/proxy/router.go` (rewrite `List()` and `Route()`); `kapeproxy/internal/proxy/router_test.go` (extend with the worked example).

**Error handling.** Stdlib `path.Match` returns `ErrBadPattern` when a glob is malformed. The router treats malformed globs as "matches nothing" (logged once per pattern at startup so operators see the misconfiguration early). It does not panic and does not refuse to start — one misconfigured glob must not take the whole proxy offline.

### 3.B Image-tag defaults — `latest` / `:local` / chart pin

| Surface | Old (PR #57) | New |
|---|---|---|
| Operator default (`config.go:71`) | `"0.7.0"` | `"latest"` |
| Operator default (`config.go:100`, `WithDefaults`) | `"stub"` | `"latest"` |
| Playground compose | `kape/kapeproxy:0.7.0` | `kape/kapeproxy:local` |
| Tiltfile | `kape/kapeproxy:0.7.0` | `kape/kapeproxy:local` |
| Helm chart values | (missing) | `kapeproxy.image.tag: "0.7.0"` (or current release) |

**Rationale.** Three different consumers want three different things from a default:

- **Operator code** runs in production-like contexts. Its in-code default should be the moving tag the project always publishes (`latest`), so the operator works on a fresh cluster without chart values.
- **Playground / Tilt** are local-dev contexts. They build the image locally and never pull from a registry. The `:local` tag is the unversioned local-dev convention and is decoupled from any release.
- **Helm chart** is the declarative deployment surface where a release pin belongs. Anyone deploying to a real cluster overrides via chart values; the chart's `values.yaml` carries the current release pin (`0.7.0` at fixup time).

Release pins do not belong in Go code or in dev tooling. The current shipped PR violates this rule in three places.

**Files touched.** `operator/domain/config/config.go` (two lines); `playground/docker-compose.playground.yml:44`; `playground/Tiltfile:21,26`; `helm/values.yaml` (new block).

### 3.C Two undone tasks

**Task 12 — remove the stub CI workflow.** `.github/workflows/kapeproxy-stub.yml` still exists on the PR #57 branch. It is currently `workflow_dispatch`-only (so it does not run on every main merge, but it is still a maintained artifact that publishes `kapeproxy:stub` images on demand). The slice-5 stub binary is being removed by slice-7's Task 12; its CI workflow must go with it.

**Task 14 — add the `kapeproxy` block to `helm/values.yaml`.** The chart's `values.yaml` enumerates `operator`, `taskService`, `runtime`, `dashboard`, and `adapters` image references. There is no `kapeproxy:` block. After this fixup, the chart owns the release pin:

```yaml
kapeproxy:
  image:
    repository: kape/kapeproxy
    tag: "0.7.0"
    pullPolicy: IfNotPresent
```

If the helm chart's deployment templates need to consume this new value (the operator passes the image ref through `kape-config`, so chart values typically flow into a ConfigMap rather than a Deployment env var directly), the implementer must wire that — see the plan's Task 5 for the read-and-verify step. Today the `helm/templates/operator/` directory is a placeholder (`.gitkeep` only), so no template edits are required by this fixup; the values block is the canonical declaration regardless.

---

## 4. Schema / Contract Changes

The `allowedTools` field on `KapeTool.spec.mcp` changes its **interpretation** from "list of exact tool names with empty = expose all" (implicit, never stated; partially encoded in D14) to "list of glob patterns evaluated with stdlib `path.Match`, where missing/empty means **deny all** and `[\"*\"]` means allow all". The YAML shape is unchanged; the *meaning* of omitting the field is now opposite to D14:

- The CRD field stays `[]string`.
- The `kapeproxy-config` ConfigMap YAML format does not change.
- Exact-name allowlists remain valid because a name with no glob metacharacters (`*`, `?`, `[`) matches only itself under `path.Match`.
- **Omitting `allowedTools` now means "expose nothing from this upstream"** rather than "expose everything". Operators relying on the previous "omit = expose all" behaviour (from D14 or from the kapetool sidecar precedent) must migrate by writing `allowedTools: ["*"]` explicitly — or, preferably, an explicit minimum-privilege allowlist such as `["k8s_*"]`.

No CRD version bump, no admission webhook update, no operator-side render change (the operator may continue to omit the field for KapeTools with no `allowedTools` — kapeproxy will now interpret that omission as deny-all, which is the new safe default). The behaviour change visible to a user with an existing `allowedTools: ["foo_bar"]` is: previously the kapeproxy would emit `up__foo_bar` even if the upstream didn't have `foo_bar`; now `up__foo_bar` is only emitted (and only callable) if the upstream actually advertises `foo_bar`. The behaviour change visible to a user with **no** `allowedTools` field is much larger: the upstream now contributes zero tools instead of all of them.

---

## 5. Design Decisions

Extends the IMPLEMENTATION-SPEC D-series (last entry: D15). These decisions are added in this fixup spec; the IMPLEMENTATION-SPEC is updated to reference this document as the source of truth. D20 explicitly **supersedes D14**.

### D16 — `allowedTools` is a deny-by-default glob list intersected with the upstream tool list

`allowedTools` entries are shell-style glob patterns evaluated with stdlib `path.Match`. Both `tools/list` and `tools/call` compute the exposed set as the intersection of upstream-advertised tools with the union of glob matches. A `nil`, omitted, or empty `allowedTools` is an empty set of globs and so exposes **nothing** (deny-by-default; see D20). `["*"]` is the explicit opt-in to expose every upstream tool. Both paths enforce the same rule: `tools/list` simply omits non-matching tools; `tools/call` rejects them with MCP error `-32601` (method not found). Malformed globs are logged at startup and treated as matching nothing; one bad pattern cannot take the proxy offline.

**Rationale.** The original "list of exact names emitted verbatim" rule produced two real bugs: `tools/list` could advertise tools the upstream cannot serve, and `tools/call` could accept calls to tools that do not exist upstream. Glob-intersection makes the two paths agree and lets operators write `["k8s_*"]` instead of enumerating every tool by hand. Deny-by-default (D20) closes the implicit "expose all" hole left by D14.

### D17 — Operator in-code default for `kapeproxy` image version is `latest`

`KapeproxyImageVersion` defaults to `"latest"` in `operator/domain/config/config.go` (both the inline default in `KapeproxyImageRef()` and the `WithDefaults()` assignment). Release pins live in `helm/values.yaml`, not in Go code.

**Rationale.** The IMPLEMENTATION-SPEC §2.1 already mandates `latest` as the operator's default; the PR-time release pin is a deployment-surface concern. Hardcoding `0.7.0` in Go code creates a release-coupling that has to be unwound every time the kapeproxy image bumps.

### D18 — Playground and Tilt reference `kape/kapeproxy:local`

`playground/docker-compose.playground.yml` and `playground/Tiltfile` build and reference `kape/kapeproxy:local`. The `:local` tag is the unversioned local-dev convention; it never appears in a registry and is decoupled from any release version.

**Rationale.** Local dev should not pretend to be a release. The previous `:0.7.0` tag in playground files looked like a pull from a registry but was actually a local build — confusing, and it meant every release bump had to touch dev tooling.

### D19 — The slice-5 stub binary and its CI pipeline are removed in this fixup

`.github/workflows/kapeproxy-stub.yml` is deleted as part of this fixup, completing slice-7's Task 12 (the binary itself was already removed by PR #57; the workflow was missed). No future PR should push `kape/kapeproxy:stub` to any registry.

**Rationale.** D2 + R1 in the original IMPLEMENTATION-SPEC explicitly time-bounded the stub to "removed in slice 7". A CI workflow that still publishes a `kape/kapeproxy:stub` tag — even on `workflow_dispatch` — keeps that artifact alive and creates a path for it to drift back into use.

### D20 — `allowedTools` is deny-by-default; supersedes D14

**Supersedes** [`docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md` D14, line 484](../../roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md). D14 stated that `allowedTools: []` (or omitted) means "expose all" — matching the legacy kapetool sidecar precedent. D20 flips this: a `nil`, omitted, or `[]` `allowedTools` exposes **zero** tools from the upstream. The operator opts into "expose all" explicitly by writing `allowedTools: ["*"]` (the single glob `*` matches every flat tool name returned by `upstream.ListTools()`).

The rule applies uniformly to both `tools/list` (the upstream contributes no namespaced names) and `tools/call` (rejected with MCP error `-32601` because `Route()` returns `(nil, false)`).

**Rationale.** Security posture for an audited proxy: an operator must opt in to forwarding traffic to an upstream, not opt out. "Expose all by default" makes it trivially easy to misconfigure a handler — adding a new upstream with no `allowedTools` would have silently exposed every tool the upstream advertises. The previous behaviour also leaked information about the upstream's tool catalog to any client that called `tools/list` without the operator having ever sanctioned that exposure. Deny-by-default forces the question to be answered at configuration time.

**Migration.** Existing `KapeTool` resources whose `spec.mcp.allowedTools` is omitted or `[]` will, after this fixup ships, contribute **no** tools through kapeproxy. To retain the old D14 "expose all" behaviour, operators must add `allowedTools: ["*"]` explicitly. Preferred migration is to write a minimum-privilege allowlist (e.g. `["k8s_*"]`) — `["*"]` is supported but discouraged.

**Operator-side impact.** The operator's `renderKapeproxyConfig` does **not** change behaviour. It may continue to omit `allowedTools` from the rendered `kapeproxy-config` ConfigMap when the KapeTool's `spec.mcp.allowedTools` is empty — the on-the-wire YAML shape is unchanged, and kapeproxy now interprets that omission as deny-all (the new safe default). The semantic flip lives entirely in kapeproxy's router.

---

## 6. Out of Scope

This fixup is intentionally narrow. The following are **not** touched:

- **kapeproxy runtime internals** — JSON-RPC server (`server.go`), upstream client (`upstream.go`), redaction (`redaction.go`), audit logging (`audit.go`), OTEL bootstrap (`otel.go`), config parser (`config.go`).
- **MCP SDK** — no upgrade, no API surface changes.
- **Operator reconcilers** — `KapeHandlerReconciler`, `KapeToolReconciler`, `KapeSchemaReconciler` are all unaffected. The `kapeproxy-config` ConfigMap render path is unaffected because the YAML shape is unchanged (D16 only changes how the proxy *interprets* `allowedTools`, not how the operator *writes* it).
- **CRD schemas** — no CRD version bump; no validation webhook update.
- **kapeproxy module dependencies** — no new imports beyond stdlib `path`. (The router already exists; this is a rewrite of two methods, not a new package.)
- **End-to-end envtest** — the existing slice-7 e2e test continues to assert the same observable outcomes. If the test happened to assert "allowedTools entries appear verbatim in `tools/list`", it gets updated as part of Task 1's red-green loop.

---

## 7. Migration and Backward Compatibility

### `allowedTools` semantic change — BREAKING

> **Breaking change.** This fixup flips `allowedTools` from "omit means expose all" (D14) to "omit means expose nothing" (D20). Any `KapeTool` whose `spec.mcp.allowedTools` is omitted or `[]` will, after the fixup ships, contribute **zero** tools through kapeproxy. Operators must touch every such KapeTool to either (a) write an explicit minimum-privilege allowlist (preferred) or (b) add `allowedTools: ["*"]` (legacy "expose all", discouraged). There is no soft-rollover or grace period — the behaviour change is immediate at upgrade.

Existing `KapeTool.spec.mcp.allowedTools` values written as *populated* exact-name lists (e.g. `["query_dashboards", "get_alert"]`) continue to work unchanged because `path.Match("query_dashboards", "query_dashboards") == true`. The breaking case is specifically the empty/omitted form.

Migration steps for operators:

1. **Inventory.** `kubectl get kapetool -A -o jsonpath='{range .items[?(!@.spec.mcp.allowedTools)]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}'` (or equivalent) lists every KapeTool that will lose all exposed tools post-upgrade.
2. **Decide per tool.** For each, pick:
   - **Preferred:** write an explicit allowlist matching only the tools that handler needs (e.g. `["k8s_get_*", "helm_list"]`). Minimum-privilege is the point of D20.
   - **Legacy escape hatch:** write `allowedTools: ["*"]` to retain pre-D20 "expose all" semantics. Discouraged because it re-introduces the security hole D20 closed.
3. **Apply before the kapeproxy upgrade rolls.** Operators can edit KapeTools at any time — the operator's `renderKapeproxyConfig` faithfully translates the new field value into `kapeproxy-config` regardless of which kapeproxy version is running. The change only "matters" once the new kapeproxy binary is in the handler pod.

Additionally, the same in-allowlist behaviour change from the original spec still applies: a stale allowlist entry that names a tool the upstream no longer advertises will silently drop out of `tools/list` and `tools/call` will reject it (instead of accepting the call and letting the upstream return an unknown-tool error). This surfaces stale config locally instead of routing dead calls.

### Operator-side render path is unchanged

The operator's `renderKapeproxyConfig` does **not** need behaviour changes for D20. The existing "omit `allowedTools` from the rendered YAML when the slice is empty" optimisation remains correct on the wire — kapeproxy now interprets that omission as deny-all, which is the safe default under D20. Existing operator `kapeproxy-config` golden tests stay green; no golden file updates are required.

The operator's envtest scenarios *should* gain a small assertion (see plan Task 5b) that a KapeTool with a populated `allowedTools` glob exposes exactly the intersection through kapeproxy. This is light-touch coverage of the contract from the operator's vantage point and is flagged as optional-but-recommended in the plan.

### Operator in-code default change (`0.7.0` → `latest`)

Operators running the operator binary with no `kape-config` override for `kapeproxy.version` will get `kape/kapeproxy:latest` after upgrading, where previously they got `kape/kapeproxy:0.7.0`. If pinning to a specific kapeproxy release is required, the operator must either:

1. Set `kapeproxy.version: 0.7.0` in the `kape-config` ConfigMap (existing override path), or
2. Deploy via the Helm chart and let chart values flow through (chart's `values.yaml` carries the pin per D17).

This is a one-line operator-facing change. Call it out in the implementation PR's description so an upgrader notices.

### Playground `:0.7.0` → `:local`

No production impact — playground tags never reached a registry. Local dev users will need a fresh `tilt up` / `podman compose build` to pick up the renamed local image; the old `kape/kapeproxy:0.7.0` local image may still be on their machine and can be removed with `podman rmi kape/kapeproxy:0.7.0`.

---

## 8. References

### Files / lines superseded

- [`docs/roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md:381`](../../roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md) — the wrong rule ("`List()` reports the names from `allowedTools` when set"). This line gets a note pointing at the present spec.
- Supersedes D14 in [`docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md:484`](../../roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md) (now D20 in this fixup) — `allowedTools` flips from "expose all when empty" to deny-by-default.
- `kapeproxy/internal/proxy/router.go:50-55,81-84` (PR #57 branch) — current implementation of `Route()` and `List()` that implements the wrong rule.
- `operator/domain/config/config.go:71,100` (PR #57 branch) — wrong defaults (`0.7.0` and `stub`).
- `playground/docker-compose.playground.yml:44`, `playground/Tiltfile:21,26` (PR #57 branch) — `kape/kapeproxy:0.7.0` references.

### Files / lines extended

- [`docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md`](../../roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md) — D-series gets D16–D20 (or a single reference to this spec as the source of truth for those decisions). D20 explicitly supersedes D14.

### Original slice-7 tasks closed by this fixup

- Task 12 (line ~2307): remove `.github/workflows/kapeproxy-stub.yml` (the stub binary was removed; the workflow was not).
- Task 14 (line ~2440): add the `kapeproxy` block to `helm/values.yaml`.

### Related

- [PR #57 — slice 7 production kapeproxy binary](https://github.com/dzungtr/kape/pull/57)
- [Original slice-7 plan](../../roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md)
- [Phase 6 IMPLEMENTATION-SPEC](../../roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md)
