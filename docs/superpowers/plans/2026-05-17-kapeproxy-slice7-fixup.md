# kapeproxy Slice 7 Fixup — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Each task follows red-test → green-code → verify per superpowers:test-driven-development.

**Goal:** Apply the corrections described in [`docs/superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md`](../specs/2026-05-17-kapeproxy-slice7-fixup.md) on top of PR #57's `feat/phase6-slice7-kapeproxy-binary` branch — glob-intersect tools filter, `latest` / `:local` / chart-pin image defaults, plus completing slice-7 Tasks 12 and 14.

**Architecture:** Two Go edits (`kapeproxy/internal/proxy/router.go`, `operator/domain/config/config.go`), three YAML/file edits (playground compose, Tiltfile, helm values), one workflow deletion, and two docs touch-ups. No new packages, no new module dependencies beyond stdlib `path`.

**Tech Stack:** Go 1.25 (kapeproxy module), Go 1.24 (operator module), stdlib `path.Match`, existing test deps (`testify`, zerolog).

**Worktree:** Implementer should branch from PR #57's branch (`feat/phase6-slice7-kapeproxy-binary`) into a fresh worktree, e.g. `.worktrees/feat-kapeproxy-slice7-fixup`, so the fixup can land as a follow-up commit / push on the same PR — or as a stacked PR if PR #57 has already merged by then. The exact worktree path is implementer's choice; all commands below use `<wt>` as a stand-in.

---

## File Map

**kapeproxy module — modified**

| File | Action | Purpose |
|---|---|---|
| `kapeproxy/internal/proxy/router.go` | Modify | Rewrite `List()` and `Route()` to use glob-intersect with `path.Match` |
| `kapeproxy/internal/proxy/router_test.go` | Modify | Replace `TestRouter_NamespacedNames_WithAllowlist` + `TestRouter_UnavailableUpstream_StillExposesNames`; add new glob-intersect tests |

**operator module — modified**

| File | Action | Purpose |
|---|---|---|
| `operator/domain/config/config.go` | Modify | `KapeproxyImageRef()` default `"0.7.0"` → `"latest"`; `WithDefaults()` `"stub"` → `"latest"` |
| `operator/domain/config/config_test.go` | Create/Modify | Assert `KapeproxyImageRef()` returns `kape/kapeproxy:latest` when no override |

**playground / Tilt — modified**

| File | Action | Purpose |
|---|---|---|
| `playground/docker-compose.playground.yml` | Modify | Line 44: `kape/kapeproxy:0.7.0` → `kape/kapeproxy:local` |
| `playground/Tiltfile` | Modify | Lines 21, 26: `kape/kapeproxy:0.7.0` → `kape/kapeproxy:local` |

**helm chart — modified**

| File | Action | Purpose |
|---|---|---|
| `helm/values.yaml` | Modify | Add `kapeproxy:` block with `image: { repository, tag, pullPolicy }` |
| `helm/templates/operator/*` | Read-only check | Verify nothing currently consumes the values; wire if it does |

**CI workflows — removed**

| File | Action | Purpose |
|---|---|---|
| `.github/workflows/kapeproxy-stub.yml` | Delete | Stub binary is gone (slice-7 Task 12); its workflow goes with it |

**docs — modified**

| File | Action | Purpose |
|---|---|---|
| `docs/roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md` | Modify | Line 381: add a note that the rule is superseded; link to fixup spec |
| `docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md` | Modify | Append D16–D19 referring to the fixup spec as source of truth |

---

## Pre-flight

- [ ] **Step 1: Confirm worktree branched from the right base**

```bash
git -C <wt> status
git -C <wt> log --oneline -5
```

Expected: clean tree, HEAD is on or descended from `feat/phase6-slice7-kapeproxy-binary` (or `main` if PR #57 has merged — in that case rebase this fixup branch on `origin/main` before continuing).

- [ ] **Step 2: Confirm the divergences are still present**

```bash
git -C <wt> grep -n "kape/kapeproxy:0.7.0" -- playground/
git -C <wt> grep -n "\"0.7.0\"\|\"stub\"" -- operator/domain/config/config.go
git -C <wt> ls .github/workflows/kapeproxy-stub.yml
```

Expected: all three return matches. If any do not match, somebody has fixed part of this already — stop and reconcile before proceeding (re-read the fixup spec and trim the relevant task).

---

## Task 1: Tools filter — red tests (glob-intersect)

**Files:**
- Modify: `kapeproxy/internal/proxy/router_test.go`

The existing tests in `router_test.go` accidentally encode the wrong rule. `TestRouter_NamespacedNames_WithAllowlist` asserts that an allowlist of `["query_dashboards", "get_alert"]` exposes those two names whether or not the upstream has them — which is the bug. `TestRouter_UnavailableUpstream_StillExposesNames` asserts the allowlist is the source of truth even when the upstream is unreachable — also the wrong rule under D16.

This task adds the two new tests that pin the corrected vision, and updates the two misaligned existing tests so they assert the new contract. Run before any code change; both new and updated tests must fail before Task 2.

- [ ] **Step 1: Add `TestRouterList_GlobIntersectsUpstream`**

In `kapeproxy/internal/proxy/router_test.go`, add:

```go
func TestRouterList_GlobIntersectsUpstream(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: []string{"k8s_*"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "k8s_list_namespaces", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	assert.ElementsMatch(t,
		[]string{"up__k8s_get_pods", "up__k8s_list_namespaces"},
		r.List(),
		"only k8s_* tools that the upstream actually advertises should be exposed",
	)
}

func TestRouterList_ExactNameNotOnUpstream_Dropped(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "sse",
				AllowedTools: []string{"k8s_get_pods", "nonexistent"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "k8s_list_namespaces", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	assert.ElementsMatch(t,
		[]string{"up__k8s_get_pods"},
		r.List(),
		"exact-name allowlist entries that don't match any upstream tool are silently dropped",
	)
}
```

- [ ] **Step 2: Add `TestRouterRoute_GlobIntersectsUpstream`**

```go
func TestRouterRoute_GlobIntersectsUpstream(t *testing.T) {
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: []string{"k8s_*"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "k8s_list_namespaces", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	// Matches glob AND exists on upstream → routable.
	e, ok := r.Route("up__k8s_get_pods")
	require.True(t, ok)
	assert.Equal(t, "k8s_get_pods", e.OriginalName)

	// Exists on upstream but does NOT match any glob → not routable.
	_, ok = r.Route("up__helm_install")
	assert.False(t, ok, "helm_install is on the upstream but no glob matches it")

	// Matches glob (vacuously) but does NOT exist on upstream → not routable.
	_, ok = r.Route("up__k8s_nonexistent")
	assert.False(t, ok, "k8s_nonexistent matches k8s_* but is not on the upstream")
}

func TestRouterRoute_MalformedGlob_DoesNotPanic(t *testing.T) {
	// path.Match returns ErrBadPattern for an unterminated character class.
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "sse",
				AllowedTools: []string{"[bad"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{name: "up", tools: []string{"anything"}, avail: true},
	}
	r := proxy.NewRouter(cfg, ups)
	assert.Empty(t, r.List(), "malformed glob matches nothing; router must not panic")
	_, ok := r.Route("up__anything")
	assert.False(t, ok)
}
```

- [ ] **Step 3: Update `TestRouter_NamespacedNames_WithAllowlist`**

The existing test asserts `delete_dashboard` is filtered out — that part stays correct. But it relied on the literal-emit behaviour to pass with an upstream that advertised the allowed names verbatim. Re-read the test; if the upstream's `tools` already contains every allowlist entry (it does: `["query_dashboards", "get_alert", "delete_dashboard"]`), the test passes under the new rule too — no change needed beyond verifying it after Task 2.

- [ ] **Step 4: Update `TestRouter_UnavailableUpstream_StillExposesNames`**

Under D16 this test's assertion is wrong. An unavailable upstream returns an empty `ListTools()`, so the intersection is empty. Rename and rewrite:

```go
func TestRouter_UnavailableUpstream_ExposesNothing(t *testing.T) {
	// Under D16, the exposed set is intersection(upstream.ListTools, globs).
	// An unreachable upstream has ListTools() == nil, so nothing can be exposed
	// regardless of what's in allowedTools. tools/call on those names will
	// likewise fail at Route() (not at upstream call-time).
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"down-mcp": {
				URL: "http://down:8080", Transport: "sse",
				AllowedTools: []string{"foo"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"down-mcp": &fakeUpstream{name: "down-mcp", tools: nil, avail: false},
	}
	r := proxy.NewRouter(cfg, ups)
	assert.Empty(t, r.List(),
		"D16: unreachable upstream → empty ListTools → empty intersection → nothing exposed")
	_, ok := r.Route("down-mcp__foo")
	assert.False(t, ok)
}
```

- [ ] **Step 5: Run the tests; confirm they fail**

```bash
cd <wt>/kapeproxy && go test ./internal/proxy/... -run TestRouter -v 2>&1 | tail -50
```

Expected:
- `TestRouterList_GlobIntersectsUpstream` FAILS — current `List()` emits the allowlist verbatim, so `up__helm_install` is missing (good) but `helm_install` isn't in the allowlist either, so this might pass by coincidence. Verify by adding a glob entry that the current code can't possibly handle: `k8s_*` is not in `upstream.ListTools()`, so the current code emits `up__k8s_*` literally and the test fails on the namespacing.
- `TestRouterList_ExactNameNotOnUpstream_Dropped` FAILS — current code emits `up__nonexistent` verbatim.
- `TestRouterRoute_GlobIntersectsUpstream` FAILS — current `Route()` does string-equality with allowlist (not glob) and does not consult `upstream.ListTools()`.
- `TestRouterRoute_MalformedGlob_DoesNotPanic` PASSES coincidentally (current code never calls `path.Match`); will still pass after Task 2 — the assertion is what matters.
- `TestRouter_UnavailableUpstream_ExposesNothing` FAILS — current code emits `down-mcp__foo` verbatim from the allowlist.

If any of these unexpectedly pass, re-read the current `router.go` and revise the test to actually pin the new rule.

- [ ] **Step 6: Commit the red tests**

```bash
git -C <wt> add kapeproxy/internal/proxy/router_test.go
git -C <wt> commit -m "test(kapeproxy/router): pin glob-intersect tools-filter semantics (failing)"
```

---

## Task 2: Tools filter — green code (glob-intersect)

**Files:**
- Modify: `kapeproxy/internal/proxy/router.go`

Rewrite `List()` and `Route()` so both consult the upstream's real tool list and apply glob-matching against `allowedTools`. Add a small helper that validates patterns once at construction and logs malformed ones, so the per-call path never reports `ErrBadPattern`.

- [ ] **Step 1: Validate globs at construction**

In `NewRouter`, after building the receiver, iterate every `upCfg.AllowedTools` entry and call `path.Match(p, "")` (or any non-empty dummy) to detect `ErrBadPattern`. On error, emit a single zerolog warn-level line per malformed pattern (`router.glob_pattern_invalid` with fields `upstream`, `pattern`, `error`) and remember the pattern so the per-call hot path can skip it cheaply. Suggested shape:

```go
// router.go (illustrative)
func NewRouter(cfg *Config, upstreams map[string]Upstream) *Router {
	r := &Router{cfg: cfg, upstreams: upstreams}
	for upName, upCfg := range cfg.Upstreams {
		for _, p := range upCfg.AllowedTools {
			if _, err := path.Match(p, ""); err != nil {
				log.Warn().
					Str("upstream", upName).
					Str("pattern", p).
					Err(err).
					Msg("router.glob_pattern_invalid; treating as match-nothing")
			}
		}
	}
	return r
}
```

(Implementer's choice whether to filter the bad patterns out of the slice or skip them inline in `matchesAny`. The latter is simpler and keeps the config structure intact for debugging.)

- [ ] **Step 2: Add a `matchesAny` helper**

```go
// matchesAny reports whether tool matches any of the glob patterns.
// Patterns that produce ErrBadPattern are skipped silently (already logged at startup).
func matchesAny(patterns []string, tool string) bool {
	for _, p := range patterns {
		ok, err := path.Match(p, tool)
		if err == nil && ok {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: Rewrite `List()`**

```go
func (r *Router) List() []string {
	var out []string
	for upName, upCfg := range r.cfg.Upstreams {
		up, ok := r.upstreams[upName]
		if !ok {
			continue
		}
		for _, t := range up.ListTools() {
			if upCfg.AllowedTools == nil || matchesAny(upCfg.AllowedTools, t) {
				out = append(out, Namespace(upName, t))
			}
		}
	}
	return out
}
```

Key differences from PR #57: enumerates `up.ListTools()` first (so the upstream is the source of truth), then filters by glob match. The `nil` case still exposes everything — D14 unchanged.

- [ ] **Step 4: Rewrite `Route()`**

```go
func (r *Router) Route(namespaced string) (*Entry, bool) {
	for upName, upCfg := range r.cfg.Upstreams {
		prefix := upName + NamespaceSeparator
		if !strings.HasPrefix(namespaced, prefix) {
			continue
		}
		original := namespaced[len(prefix):]
		up, ok := r.upstreams[upName]
		if !ok {
			return nil, false
		}
		// D16: original must exist on upstream AND match at least one glob (or allowlist == nil).
		if !contains(up.ListTools(), original) {
			return nil, false
		}
		if upCfg.AllowedTools != nil && !matchesAny(upCfg.AllowedTools, original) {
			return nil, false
		}
		audit := true
		if upCfg.Audit != nil {
			audit = *upCfg.Audit
		}
		return &Entry{
			Upstream:     up,
			OriginalName: original,
			Redaction:    upCfg.Redaction,
			Audit:        audit,
		}, true
	}
	return nil, false
}
```

(The existing `contains` helper stays as-is — used here for the upstream membership check.)

- [ ] **Step 5: Add the `path` and `strings` imports**

```go
import (
	"path"
	"strings"

	"github.com/rs/zerolog/log"
)
```

- [ ] **Step 6: Run the tests; confirm green**

```bash
cd <wt>/kapeproxy && go test ./internal/proxy/... -v 2>&1 | tail -60
```

Expected: every `TestRouter*` test passes, including the four added in Task 1 and the unchanged ones. If the integration test (`kapeproxy/integration_test.go`) breaks because it relied on the old verbatim semantics, update its mock upstream's `ListTools()` to include the names being exercised and re-run. Note this in the commit message.

- [ ] **Step 7: Commit**

```bash
git -C <wt> add kapeproxy/internal/proxy/router.go
git -C <wt> commit -m "feat(kapeproxy/router)!: allowedTools is a glob list intersected with upstream.ListTools (D16)"
```

The `!` marks the semantic-change scope per the spec (§4); the behaviour is backward-compatible for exact-name allowlists but the contract narrowed.

---

## Task 3: Operator default image version → `latest`

**Files:**
- Modify: `operator/domain/config/config.go`
- Create or Modify: `operator/domain/config/config_test.go`

- [ ] **Step 1: Write the failing test**

If `operator/domain/config/config_test.go` does not exist, create it:

```go
package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
)

func TestKapeproxyImageRef_DefaultIsLatest(t *testing.T) {
	c := domainconfig.KapeConfig{} // nothing set
	assert.Equal(t, "kape/kapeproxy:latest", c.KapeproxyImageRef(),
		"D17: in-code default for kapeproxy is :latest, not a release pin")
}

func TestKapeproxyImageRef_WithDefaults_IsLatest(t *testing.T) {
	c := domainconfig.KapeConfig{}.WithDefaults()
	assert.Equal(t, "latest", c.KapeproxyImageVersion,
		"D17: WithDefaults must set :latest, not :stub or a release pin")
	assert.Equal(t, "kape/kapeproxy:latest", c.KapeproxyImageRef())
}
```

- [ ] **Step 2: Run the test; confirm it fails**

```bash
cd <wt>/operator && go test ./domain/config/... -v 2>&1 | tail -20
```

Expected: `TestKapeproxyImageRef_DefaultIsLatest` fails (got `kape/kapeproxy:0.7.0`); `TestKapeproxyImageRef_WithDefaults_IsLatest` fails (got `stub`).

- [ ] **Step 3: Edit `operator/domain/config/config.go`**

Two changes:

- Line 71 (inside `KapeproxyImageRef`): `ver = "0.7.0"` → `ver = "latest"`.
- Line 100 (inside `WithDefaults`): `c.KapeproxyImageVersion = "stub"` → `c.KapeproxyImageVersion = "latest"`.

- [ ] **Step 4: Run the test; confirm green**

```bash
cd <wt>/operator && go test ./domain/config/... -v 2>&1 | tail -20
```

Expected: both new tests pass; nothing else regresses (`go test ./...` from the operator module root should also pass).

- [ ] **Step 5: Commit**

```bash
git -C <wt> add operator/domain/config/config.go operator/domain/config/config_test.go
git -C <wt> commit -m "fix(operator/config): kapeproxy in-code default is :latest, not a release pin (D17)"
```

---

## Task 4: Playground + Tilt → `kape/kapeproxy:local`

**Files:**
- Modify: `playground/docker-compose.playground.yml` (line 44)
- Modify: `playground/Tiltfile` (lines 21 and 26)

No unit test — the change is to dev tooling and there is no automated harness for either file. Verification is manual.

- [ ] **Step 1: Edit the compose file**

In `playground/docker-compose.playground.yml`, replace the `image:` line under the `kapeproxy:` service:

```diff
   kapeproxy:
     build:
       context: ../kapeproxy
       dockerfile: ../kapeproxy/Dockerfile
-    image: kape/kapeproxy:0.7.0
+    image: kape/kapeproxy:local
     profiles:
       - build-only
```

- [ ] **Step 2: Edit the Tiltfile**

In `playground/Tiltfile`, the comment block and the `docker_build` call both reference `0.7.0`:

```diff
 # ── kapeproxy (Go) ───────────────────────────────────────────────────────────
-# Builds kape/kapeproxy:0.7.0 locally so the operator can deploy handler pods
+# Builds kape/kapeproxy:local locally so the operator can deploy handler pods
 # without pulling from a remote registry.  No compose service — the image is
 # used by the operator-managed Deployment pods inside the k3d cluster.
 docker_build(
-    'kape/kapeproxy:0.7.0',
+    'kape/kapeproxy:local',
     context='../kapeproxy',
     dockerfile='../kapeproxy/Dockerfile',
 )
```

- [ ] **Step 3: Sanity-check no `0.7.0` references remain under `playground/`**

```bash
git -C <wt> grep -n "kape/kapeproxy:0.7.0" -- playground/
```

Expected: no output.

- [ ] **Step 4: Manual verification (implementer's local machine)**

```bash
cd <wt>/playground && podman compose build kapeproxy
# Then in a separate shell:
cd <wt>/playground && tilt up
```

Expected: both commands produce/reference `kape/kapeproxy:local`. Tilt UI shows `docker_build → kape/kapeproxy:local`. Note the manual-verification result in the PR description.

- [ ] **Step 5: Commit**

```bash
git -C <wt> add playground/docker-compose.playground.yml playground/Tiltfile
git -C <wt> commit -m "chore(playground): use kape/kapeproxy:local for dev tooling (D18)"
```

---

## Task 5: `helm/values.yaml` — add `kapeproxy` block

**Files:**
- Modify: `helm/values.yaml`
- Read-only: `helm/templates/operator/*`, `helm/templates/crds/*` (verify whether anything consumes the new values)

- [ ] **Step 1: Inspect current helm templates**

```bash
git -C <wt> ls-tree -r HEAD -- helm/templates/
```

Expected at this fixup's time: `.gitkeep` placeholders only — no real templates. If real templates have landed by the time this task runs, read `helm/templates/operator/` and `helm/templates/crds/` to find any reference to `.Values.kapeproxy.image.*`. If found, update those references to match the new block shape (`.repository`, `.tag`, `.pullPolicy`).

- [ ] **Step 2: Add the `kapeproxy` block to `helm/values.yaml`**

Append after the existing `adapters:` block, before `nats:`:

```yaml
kapeproxy:
  image:
    repository: kape/kapeproxy
    tag: "0.7.0"
    pullPolicy: IfNotPresent
```

`0.7.0` is the current release pin (the same value previously hardcoded in Go). Future release bumps edit only this line.

- [ ] **Step 3: Verify chart still renders**

```bash
cd <wt> && helm template helm/ 2>&1 | tail -20
```

Expected: chart renders without error. (If `helm` is not installed, fall back to `helm lint helm/` or skip and rely on CI.)

- [ ] **Step 4: Commit**

```bash
git -C <wt> add helm/values.yaml
git -C <wt> commit -m "chore(helm): pin kapeproxy image in chart values, not Go code (D17/D18)"
```

If `helm/templates/` updates were needed, add them to the same commit.

---

## Task 6: Remove the stub CI workflow

**Files:**
- Delete: `.github/workflows/kapeproxy-stub.yml`

- [ ] **Step 1: Verify no other workflow references the stub**

```bash
git -C <wt> grep -n "kapeproxy:stub\|kapeproxy-stub" -- .github/
```

Expected: matches only inside `.github/workflows/kapeproxy-stub.yml` itself (nothing else depends on it).

- [ ] **Step 2: Delete the workflow**

```bash
git -C <wt> rm .github/workflows/kapeproxy-stub.yml
```

- [ ] **Step 3: Repo-wide sanity check**

```bash
git -C <wt> grep -n "kape/kapeproxy:stub\|kapeproxy:stub-" 2>&1 | grep -v "\.git" || echo "clean"
```

Expected: `clean` (or only matches inside the spec/plan docs created in this fixup which legitimately discuss the removal — those are fine).

- [ ] **Step 4: Commit**

```bash
git -C <wt> commit -m "chore(ci): remove transitional kapeproxy-stub workflow (slice-7 Task 12, D19)"
```

---

## Task 7: Spec/plan housekeeping

**Files:**
- Modify: `docs/roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md` (line 381 area)
- Modify: `docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md` (D-series after D15)

- [ ] **Step 1: Annotate the wrong rule in the slice-7 plan**

Open `docs/roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md`. The paragraph starting around line 381:

> "For `List()`, the router reports the names from `allowedTools` when set, and asks the upstream's cached `Available()` tool list otherwise."

Prepend a note (do not delete the historical text — it explains why the PR #57 code looks the way it does):

```markdown
> **Superseded 2026-05-17 by [`docs/superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md`](../../../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md) §3.A / D16.** The rule below is the *original* slice-7 plan; the corrected rule is glob-intersect with `upstream.ListTools()`. Implementation lives under `docs/superpowers/plans/2026-05-17-kapeproxy-slice7-fixup.md`.
```

- [ ] **Step 2: Extend IMPLEMENTATION-SPEC's decision log**

Open `docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md`. After the D15 row in the Decision Log table (around line 485), add:

```markdown
| D16 | `allowedTools` is a glob-pattern list (`path.Match`) intersected with `upstream.ListTools()`; both `tools/list` and `tools/call` agree on the same exposed set | Original D14 only defined the `nil` case; the populated case was filled in wrong by the slice-7 plan. See [2026-05-17-kapeproxy-slice7-fixup spec](../../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md) §3.A |
| D17 | Operator in-code default for `kapeproxy` image version is `latest`; release pins live in `helm/values.yaml` | Spec §2.1 already mandates `latest`; release-coupling does not belong in Go. See fixup spec §3.B |
| D18 | Playground and Tilt build/reference `kape/kapeproxy:local` (unversioned local-dev tag) | Decouple dev tooling from release versions. See fixup spec §3.B |
| D19 | The slice-5 stub binary and `.github/workflows/kapeproxy-stub.yml` are both removed in the fixup (completes slice-7 Task 12) | Time-bounding the stub per D2 + R1. See fixup spec §3.C |
```

(If the implementer prefers a single "See fixup spec for D16–D19" row instead of four full rows, that is acceptable — the fixup spec is the canonical source.)

- [ ] **Step 3: Commit**

```bash
git -C <wt> add \
  docs/roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md \
  docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md
git -C <wt> commit -m "docs(phase6): annotate slice-7 plan supersession; add D16–D19 to decision log"
```

---

## Task 8: Final verification

- [ ] **Step 1: Run every Go test**

```bash
cd <wt>/kapeproxy && go test ./... 2>&1 | tail -20
cd <wt>/operator && go test ./... 2>&1 | tail -20
```

Expected: both green.

- [ ] **Step 2: Confirm `tools/list` and `tools/call` agree (router-level)**

The router tests added in Task 1 already pin this. Re-run the targeted set once more to be sure:

```bash
cd <wt>/kapeproxy && go test ./internal/proxy/... -run "TestRouterList_|TestRouterRoute_" -v
```

Expected: all listed tests pass.

- [ ] **Step 3: Confirm no `kape/kapeproxy:0.7.0` reference outside `helm/values.yaml`**

```bash
git -C <wt> grep -n "kape/kapeproxy:0.7.0\|\"0.7.0\"" -- :^helm/values.yaml :^docs/
```

Expected: empty (the only remaining `0.7.0` lives in `helm/values.yaml`; docs may still mention it for context, which is fine).

- [ ] **Step 4: Confirm no `kape/kapeproxy:stub` reference anywhere outside docs**

```bash
git -C <wt> grep -n "kape/kapeproxy:stub\|kapeproxy:stub-" -- :^docs/
```

Expected: empty.

- [ ] **Step 5: Confirm `helm template` still renders**

```bash
cd <wt> && helm template helm/ > /dev/null && echo OK
```

Expected: `OK`.

---

## Acceptance Checklist

Paste this into the implementation PR's description and tick each item before merge:

- [ ] `cd kapeproxy && go test ./...` — all green, including the new `TestRouterList_GlobIntersectsUpstream`, `TestRouterList_ExactNameNotOnUpstream_Dropped`, `TestRouterRoute_GlobIntersectsUpstream`, `TestRouterRoute_MalformedGlob_DoesNotPanic`, and rewritten `TestRouter_UnavailableUpstream_ExposesNothing`.
- [ ] `cd operator && go test ./...` — all green, including the new `TestKapeproxyImageRef_DefaultIsLatest` and `TestKapeproxyImageRef_WithDefaults_IsLatest`.
- [ ] `tools/list` and `tools/call` agree on the same exposed set (covered by router tests above; verified end-to-end if the slice-7 integration test was updated).
- [ ] `helm template helm/` renders without error and the rendered output references `kape/kapeproxy:0.7.0` (sourced from `values.yaml`).
- [ ] `git grep "kape/kapeproxy:0.7.0"` returns matches only inside `helm/values.yaml` and `docs/`.
- [ ] `git grep "kape/kapeproxy:stub\|kapeproxy:stub-"` returns matches only inside `docs/`.
- [ ] `.github/workflows/kapeproxy-stub.yml` no longer exists.
- [ ] Playground stack (`podman compose build kapeproxy`) and Tilt (`tilt up`) both build/reference `kape/kapeproxy:local` — manual verification noted in PR description.
- [ ] `docs/roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md:381` carries the supersession note linking to the fixup spec.
- [ ] `docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md` decision log includes D16–D19 (or a single forward-reference to the fixup spec).
- [ ] SBOM ritual from `kape-io/CLAUDE.md` run on `./adapters`, `./operator`, `./task-service`, `./kapeproxy` (this is a code-touching PR — the spec/plan PR does not need it, but the implementation PR does).
