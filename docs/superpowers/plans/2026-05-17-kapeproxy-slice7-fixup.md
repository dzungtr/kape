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
| `kapeproxy/internal/proxy/router.go` | Modify | Rewrite `List()` and `Route()` for deny-by-default glob-intersect with `path.Match` (D16+D20); update doc comments |
| `kapeproxy/internal/proxy/router_test.go` | Modify | Replace `TestRouter_NamespacedNames_WithAllowlist` + `TestRouter_UnavailableUpstream_StillExposesNames`; add deny-by-default tests (`TestRouterList_NilAllowlistExposesNothing`, `TestRouterList_EmptyAllowlistExposesNothing`, `TestRouterList_StarAllowlistExposesAll`, `TestRouterRoute_NilAllowlistDenies`, `TestRouterRoute_StarAllowlistAllows`) and glob-intersect tests |

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

## Task 1: Tools filter — red tests (deny-by-default glob-intersect)

**Files:**
- Modify: `kapeproxy/internal/proxy/router_test.go`

The existing tests in `router_test.go` accidentally encode the wrong rule. `TestRouter_NamespacedNames_WithAllowlist` asserts that an allowlist of `["query_dashboards", "get_alert"]` exposes those two names whether or not the upstream has them — which is the bug. `TestRouter_UnavailableUpstream_StillExposesNames` asserts the allowlist is the source of truth even when the upstream is unreachable — also the wrong rule under D16. The shipped code additionally treats a nil/empty allowlist as "expose all" (the old D14 semantics), which D20 now flips.

This task adds the tests that pin the corrected D16+D20 contract — deny-by-default for nil/empty, glob-intersect for populated, `["*"]` as the explicit opt-in to expose all — and updates the misaligned existing tests so they assert the new contract. Run before any code change; both new and updated tests must fail before Task 2.

- [ ] **Step 1: Add deny-by-default `List()` tests (D20)**

In `kapeproxy/internal/proxy/router_test.go`, add:

```go
func TestRouterList_NilAllowlistExposesNothing(t *testing.T) {
	// D20: nil allowedTools is deny-by-default — the upstream contributes nothing.
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: nil,
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

	assert.Empty(t, r.List(),
		"D20: nil allowedTools exposes zero tools (deny-by-default), regardless of what the upstream advertises")
}

func TestRouterList_EmptyAllowlistExposesNothing(t *testing.T) {
	// D20: an explicitly-empty allowedTools slice is equivalent to nil.
	// (The config parser already normalises [] → nil per kapeproxy/internal/proxy/config.go;
	// implementer: verify that normalisation still applies, and if so this test exercises the
	// normalised form. If config.go does NOT normalise, this test pins the runtime behaviour
	// for both shapes directly.)
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: []string{},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	assert.Empty(t, r.List(),
		"D20: empty allowedTools slice exposes zero tools (deny-by-default)")
}

func TestRouterList_StarAllowlistExposesAll(t *testing.T) {
	// D20 escape hatch: ["*"] is the explicit opt-in to expose every upstream tool.
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: []string{"*"},
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
		[]string{"up__k8s_get_pods", "up__k8s_list_namespaces", "up__helm_install"},
		r.List(),
		"D20: [\"*\"] is the legacy 'expose all' opt-in; matches every flat tool name")
}
```

- [ ] **Step 2: Add `TestRouterList_GlobIntersectsUpstream`**

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

- [ ] **Step 3: Add deny-by-default `Route()` tests (D20)**

```go
func TestRouterRoute_NilAllowlistDenies(t *testing.T) {
	// D20: nil allowedTools → tools/call is rejected for every namespaced name from this upstream.
	// The router returns (nil, false); the JSON-RPC server translates that into MCP error -32601.
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: nil,
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	_, ok := r.Route("up__k8s_get_pods")
	assert.False(t, ok, "D20: nil allowedTools denies tools/call even when upstream has the tool")
	_, ok = r.Route("up__anything")
	assert.False(t, ok, "D20: nil allowedTools denies tools/call for every name on this upstream")
}

func TestRouterRoute_StarAllowlistAllows(t *testing.T) {
	// D20 escape hatch: ["*"] allows every upstream tool through Route() as well as List().
	cfg := &proxy.Config{
		Upstreams: map[string]*proxy.UpstreamConfig{
			"up": {
				URL: "http://up:8080", Transport: "streamable-http",
				AllowedTools: []string{"*"},
			},
		},
	}
	ups := map[string]proxy.Upstream{
		"up": &fakeUpstream{
			name:  "up",
			tools: []string{"k8s_get_pods", "helm_install"},
			avail: true,
		},
	}
	r := proxy.NewRouter(cfg, ups)

	e, ok := r.Route("up__k8s_get_pods")
	require.True(t, ok)
	assert.Equal(t, "k8s_get_pods", e.OriginalName)

	e, ok = r.Route("up__helm_install")
	require.True(t, ok)
	assert.Equal(t, "helm_install", e.OriginalName)
}
```

- [ ] **Step 4: Add `TestRouterRoute_GlobIntersectsUpstream`**

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

- [ ] **Step 5: Update `TestRouter_NamespacedNames_WithAllowlist`**

The existing test asserts `delete_dashboard` is filtered out — that part stays correct. But it relied on the literal-emit behaviour to pass with an upstream that advertised the allowed names verbatim. Re-read the test; if the upstream's `tools` already contains every allowlist entry (it does: `["query_dashboards", "get_alert", "delete_dashboard"]`), the test passes under the new rule too — no change needed beyond verifying it after Task 2.

- [ ] **Step 6: Update `TestRouter_UnavailableUpstream_StillExposesNames`**

Under D16+D20 this test's assertion is wrong. An unavailable upstream returns an empty `ListTools()`, so the intersection is empty (and a nil allowlist would also produce empty by D20). Rename and rewrite:

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

- [ ] **Step 7: Run the tests; confirm they fail**

```bash
cd <wt>/kapeproxy && go test ./internal/proxy/... -run TestRouter -v 2>&1 | tail -80
```

Expected against the PR #57 implementation (which emits the allowlist verbatim and treats nil as "expose all"):
- `TestRouterList_NilAllowlistExposesNothing` FAILS — current code treats nil as expose-all, so the upstream's three tools are emitted instead of zero.
- `TestRouterList_EmptyAllowlistExposesNothing` FAILS — same reason (current code's "empty == expose all" branch).
- `TestRouterList_StarAllowlistExposesAll` FAILS — current code emits `up__*` literally (verbatim from the allowlist), not the three real tool names.
- `TestRouterList_GlobIntersectsUpstream` FAILS — current code emits `up__k8s_*` literally.
- `TestRouterList_ExactNameNotOnUpstream_Dropped` FAILS — current code emits `up__nonexistent` verbatim.
- `TestRouterRoute_NilAllowlistDenies` FAILS — current `Route()` treats nil allowlist as "allow anything namespaced to this upstream"; the call succeeds instead of being rejected.
- `TestRouterRoute_StarAllowlistAllows` FAILS — current `Route()` does string-equality with the allowlist, so it looks for the literal `*` and rejects real tool names.
- `TestRouterRoute_GlobIntersectsUpstream` FAILS — current `Route()` does string-equality with allowlist (not glob) and does not consult `upstream.ListTools()`.
- `TestRouterRoute_MalformedGlob_DoesNotPanic` PASSES coincidentally (current code never calls `path.Match`); will still pass after Task 2 — the assertion is what matters.
- `TestRouter_UnavailableUpstream_ExposesNothing` FAILS — current code emits `down-mcp__foo` verbatim from the allowlist.

If any of these unexpectedly pass, re-read the current `router.go` and revise the test to actually pin the new rule.

- [ ] **Step 8: Commit the red tests**

```bash
git -C <wt> add kapeproxy/internal/proxy/router_test.go
git -C <wt> commit -m "test(kapeproxy/router): pin deny-by-default glob-intersect semantics (failing)"
```

---

## Task 2: Tools filter — green code (deny-by-default glob-intersect)

**Files:**
- Modify: `kapeproxy/internal/proxy/router.go`

Rewrite `List()` and `Route()` so both consult the upstream's real tool list and apply glob-matching against `allowedTools` with deny-by-default for the empty case. Add a small helper that validates patterns once at construction and logs malformed ones, so the per-call path never reports `ErrBadPattern`. Update the doc comments on `List()` and `Route()` to state the deny-by-default semantic explicitly (D16 + D20).

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

- [ ] **Step 3: Rewrite `List()` (deny-by-default)**

```go
// List returns every namespaced tool name exposed by this proxy.
//
// D16+D20: for each upstream, the exposed set is the intersection of
// upstream.ListTools() with the union of path.Match() globs in
// allowedTools. A nil or empty allowedTools is the empty set of globs,
// which matches nothing — the upstream contributes zero tools
// (deny-by-default). Operators opt into "expose all" by writing
// allowedTools: ["*"].
func (r *Router) List() []string {
	var out []string
	for upName, upCfg := range r.cfg.Upstreams {
		up, ok := r.upstreams[upName]
		if !ok {
			continue
		}
		// D20: empty allowlist denies everything from this upstream.
		if len(upCfg.AllowedTools) == 0 {
			continue
		}
		for _, t := range up.ListTools() {
			if matchesAny(upCfg.AllowedTools, t) {
				out = append(out, Namespace(upName, t))
			}
		}
	}
	return out
}
```

Key differences from PR #57: enumerates `up.ListTools()` first (so the upstream is the source of truth), filters by glob match, and treats nil/empty `allowedTools` as deny-all (D20) rather than expose-all (D14, superseded).

- [ ] **Step 4: Rewrite `Route()` (deny-by-default)**

```go
// Route resolves a namespaced tool name (e.g. "k8s__get_pods") to the
// upstream entry that should handle it.
//
// D16+D20: returns (nil, false) — which the JSON-RPC server surfaces as
// MCP error -32601 (method not found) — when any of the following hold:
//   - the prefix matches no configured upstream
//   - the upstream is not in the upstreams map
//   - the upstream's allowedTools is nil or empty (deny-by-default per D20)
//   - the original tool name is not on upstream.ListTools()
//   - no path.Match glob in allowedTools matches the original name
//
// Both Route() and List() compute the same exposed set; a name advertised
// by List() must be acceptable to Route() (when the upstream is healthy)
// and vice versa.
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
		// D20: empty allowlist denies every call to this upstream.
		if len(upCfg.AllowedTools) == 0 {
			return nil, false
		}
		// D16: original must exist on upstream AND match at least one glob.
		if !contains(up.ListTools(), original) {
			return nil, false
		}
		if !matchesAny(upCfg.AllowedTools, original) {
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

(The existing `contains` helper stays as-is — used here for the upstream membership check.) The JSON-RPC server already maps `Route() → (nil, false)` to MCP error `-32601`; no change to the server is required for D20 — the existing rejection path is the rejection path D20 specifies.

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
git -C <wt> commit -m "feat(kapeproxy/router)!: allowedTools is deny-by-default; glob-intersect upstream.ListTools (D16+D20)"
```

The `!` marks the breaking change per the spec (§4, §7): KapeTools with empty or omitted `allowedTools` will now expose nothing instead of everything. Exact-name allowlists remain backward-compatible; the empty/omitted case is the breaking shift.

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

## Task 5b: Operator-side coverage for the D20 contract (optional, recommended)

**Files (optional):**
- Modify: `operator/internal/controller/kapehandler_controller_test.go` (or wherever the envtest scenarios live)

**Goal:** add light-touch operator-side coverage that the D20 contract holds end-to-end through `renderKapeproxyConfig` → kapeproxy. The operator's render path itself does **not** need to change — it can continue to omit `allowedTools` from the rendered `kapeproxy-config` YAML when the KapeTool's `spec.mcp.allowedTools` is empty, because kapeproxy now interprets that omission as deny-all (the new safe default). Existing `renderKapeproxyConfig` golden tests therefore stay green; no golden file updates are required.

This task adds **one** envtest scenario asserting the happy path of the new contract:

- [ ] **Step 1: Envtest — KapeTool with `allowedTools: ["k8s_*"]` exposes only matching tools**

Add a test that creates a `KapeHandler` + `KapeTool` with `spec.mcp.allowedTools: ["k8s_*"]`, asserts the handler reconciles to `KapeProxyConfigured`, and (using an in-process stub upstream that advertises `k8s_get_pods` and `helm_install`) asserts that a `tools/list` call against kapeproxy returns only `<up>__k8s_get_pods`. The exact name of the test and the stub upstream wiring follow the existing slice-7 envtest scaffolding — implementer reuses whatever pattern is already in the operator's envtest suite.

If wiring a live kapeproxy into the operator envtest would balloon scope, this task is **optional but recommended**: skip it and rely on the kapeproxy router tests from Task 1 to pin the contract. Flag in the PR description if skipped.

- [ ] **Step 2 (only if Step 1 was done): Commit**

```bash
git -C <wt> add operator/internal/controller/kapehandler_controller_test.go
git -C <wt> commit -m "test(operator/envtest): assert D20 allowedTools glob intersection via kapeproxy"
```

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

- [ ] **Step 2: Extend IMPLEMENTATION-SPEC's decision log and supersede D14**

Open `docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md`. First, mark D14 (line 484) as superseded — annotate in place rather than deleting, so the history stays legible:

```markdown
| D14 | ~~`allowedTools: []` means "expose all" — field omitted from kapeproxy-config when empty~~ **(SUPERSEDED 2026-05-17 by D20 in [2026-05-17-kapeproxy-slice7-fixup spec](../../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md): empty/omitted now means deny-all)** | Matches existing kapetool sidecar behaviour |
```

Then, after the D15 row, add:

```markdown
| D16 | `allowedTools` is a deny-by-default glob-pattern list (`path.Match`) intersected with `upstream.ListTools()`; both `tools/list` and `tools/call` agree on the same exposed set | Original D14 only defined the empty case (as "expose all", now superseded); the populated case was filled in wrong by the slice-7 plan. See [2026-05-17-kapeproxy-slice7-fixup spec](../../../superpowers/specs/2026-05-17-kapeproxy-slice7-fixup.md) §3.A |
| D17 | Operator in-code default for `kapeproxy` image version is `latest`; release pins live in `helm/values.yaml` | Spec §2.1 already mandates `latest`; release-coupling does not belong in Go. See fixup spec §3.B |
| D18 | Playground and Tilt build/reference `kape/kapeproxy:local` (unversioned local-dev tag) | Decouple dev tooling from release versions. See fixup spec §3.B |
| D19 | The slice-5 stub binary and `.github/workflows/kapeproxy-stub.yml` are both removed in the fixup (completes slice-7 Task 12) | Time-bounding the stub per D2 + R1. See fixup spec §3.C |
| D20 | **Supersedes D14.** `allowedTools` is deny-by-default — `nil`/omitted/`[]` exposes zero tools; `["*"]` is the explicit opt-in to expose all. Applies uniformly to `tools/list` (omit) and `tools/call` (reject with MCP `-32601`) | Security posture for an audited proxy: operator must opt in, not opt out. Migration: existing KapeTools relying on D14's "omit means expose all" must add `allowedTools: ["*"]` (or, preferred, a minimum-privilege allowlist). See fixup spec §5/D20 and §7 |
```

(If the implementer prefers a single "See fixup spec for D16–D20" row instead of five full rows, that is acceptable — the fixup spec is the canonical source. The D14 strike-through must stay either way.)

- [ ] **Step 3: Sweep for other D14 references**

```bash
grep -rn "\bD14\b" <wt>/docs/
```

Expected matches: only the IMPLEMENTATION-SPEC row (now annotated as superseded) and the fixup spec/plan files (which reference D14 in the context of explaining the supersession). If `grep` finds any other doc that cites D14 as a live rule (e.g. a runbook, an operator guide, the slice-7 plan's body), add a one-line forward-reference there too: "(D14 superseded by D20; see fixup spec)." Report the list of files touched in the PR description.

- [ ] **Step 4: Commit**

```bash
git -C <wt> add \
  docs/roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md \
  docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md
# plus any additional docs found in Step 3
git -C <wt> commit -m "docs(phase6): supersede D14; add D16–D20 to decision log; annotate slice-7 plan"
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

- [ ] `cd kapeproxy && go test ./...` — all green, including the new `TestRouterList_NilAllowlistExposesNothing`, `TestRouterList_EmptyAllowlistExposesNothing`, `TestRouterList_StarAllowlistExposesAll`, `TestRouterList_GlobIntersectsUpstream`, `TestRouterList_ExactNameNotOnUpstream_Dropped`, `TestRouterRoute_NilAllowlistDenies`, `TestRouterRoute_StarAllowlistAllows`, `TestRouterRoute_GlobIntersectsUpstream`, `TestRouterRoute_MalformedGlob_DoesNotPanic`, and rewritten `TestRouter_UnavailableUpstream_ExposesNothing`.
- [ ] `cd operator && go test ./...` — all green, including the new `TestKapeproxyImageRef_DefaultIsLatest` and `TestKapeproxyImageRef_WithDefaults_IsLatest`.
- [ ] `tools/list` and `tools/call` agree on the same exposed set (covered by router tests above; verified end-to-end if the slice-7 integration test was updated).
- [ ] **D20 contract:** no namespaced tool appears in `tools/list` unless its un-namespaced name is in `upstream.ListTools()` AND matches a glob in `allowedTools` (router tests above).
- [ ] **D20 contract:** a KapeTool with no `spec.mcp.allowedTools` exposes nothing via kapeproxy (validated by the optional envtest in Task 5b, or by the kapeproxy router tests if Task 5b was skipped — note which in the PR description).
- [ ] `helm template helm/` renders without error and the rendered output references `kape/kapeproxy:0.7.0` (sourced from `values.yaml`).
- [ ] `git grep "kape/kapeproxy:0.7.0"` returns matches only inside `helm/values.yaml` and `docs/`.
- [ ] `git grep "kape/kapeproxy:stub\|kapeproxy:stub-"` returns matches only inside `docs/`.
- [ ] `.github/workflows/kapeproxy-stub.yml` no longer exists.
- [ ] Playground stack (`podman compose build kapeproxy`) and Tilt (`tilt up`) both build/reference `kape/kapeproxy:local` — manual verification noted in PR description.
- [ ] `docs/roadmap/phases/06-full-operator/plans/slice-7-kapeproxy-binary.md:381` carries the supersession note linking to the fixup spec.
- [ ] `docs/roadmap/phases/06-full-operator/IMPLEMENTATION-SPEC.md` decision log includes D16–D20 (or a single forward-reference to the fixup spec) and D14 is annotated as superseded by D20.
- [ ] SBOM ritual from `kape-io/CLAUDE.md` run on `./adapters`, `./operator`, `./task-service`, `./kapeproxy` (this is a code-touching PR — the spec/plan PR does not need it, but the implementation PR does).
