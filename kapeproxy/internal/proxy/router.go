package proxy

import (
	"path"
	"strings"

	"github.com/rs/zerolog/log"
)

// Upstream is the abstraction used by Router for listing + calling tools
// on a remote MCP server. The real implementation is in upstream.go.
type Upstream interface {
	Name() string
	Available() bool
	ListTools() []string
	CallTool(ctx string, tool string, args map[string]any) (any, error)
	Close() error
}

// Entry is one routable namespaced tool.
type Entry struct {
	Upstream     Upstream
	OriginalName string // un-namespaced name on the upstream
	Redaction    *RedactionConfig
	Audit        bool
}

// Router maps namespaced names ({upstream}__{tool}) to upstreams.
type Router struct {
	cfg       *Config
	upstreams map[string]Upstream
}

// NewRouter builds a router. upstreams must have one entry per cfg.Upstreams key
// (an upstream that failed to dial at startup is still passed in with Available()=false).
//
// Each upstream's allowedTools entries are validated as path.Match glob
// patterns at construction time. Malformed patterns are logged once at
// warn level and treated as matching nothing in subsequent List() / Route()
// calls — one bad pattern must not take the whole proxy offline (D16
// error-handling clause).
func NewRouter(cfg *Config, upstreams map[string]Upstream) *Router {
	r := &Router{cfg: cfg, upstreams: upstreams}
	if cfg != nil {
		for upName, upCfg := range cfg.Upstreams {
			if upCfg == nil {
				continue
			}
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
	}
	return r
}

// NamespaceSeparator joins upstream + tool into the wire-level name.
const NamespaceSeparator = "__"

// Namespace joins upstream + tool into the wire-level name.
func Namespace(upstream, tool string) string {
	return upstream + NamespaceSeparator + tool
}

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
		if !strings.HasPrefix(namespaced, prefix) || len(namespaced) <= len(prefix) {
			continue
		}
		original := namespaced[len(prefix):]
		up, ok := r.upstreams[upName]
		if !ok {
			return nil, false
		}
		// D20: empty/nil allowlist denies every call to this upstream.
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
		// D20: empty/nil allowlist denies everything from this upstream.
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

// Upstreams returns the underlying upstream map (used by graceful shutdown).
func (r *Router) Upstreams() map[string]Upstream { return r.upstreams }

// matchesAny reports whether tool matches any of the glob patterns.
// Patterns that produce ErrBadPattern are skipped silently (already logged
// at startup by NewRouter).
func matchesAny(patterns []string, tool string) bool {
	for _, p := range patterns {
		ok, err := path.Match(p, tool)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
