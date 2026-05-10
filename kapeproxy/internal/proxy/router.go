package proxy

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
func NewRouter(cfg *Config, upstreams map[string]Upstream) *Router {
	return &Router{cfg: cfg, upstreams: upstreams}
}

// NamespaceSeparator joins upstream + tool into the wire-level name.
const NamespaceSeparator = "__"

// Namespace joins upstream + tool into the wire-level name.
func Namespace(upstream, tool string) string {
	return upstream + NamespaceSeparator + tool
}

// Route returns the entry for a namespaced tool name, or false if unknown.
func (r *Router) Route(namespaced string) (*Entry, bool) {
	for upName, upCfg := range r.cfg.Upstreams {
		prefix := upName + NamespaceSeparator
		if len(namespaced) <= len(prefix) || namespaced[:len(prefix)] != prefix {
			continue
		}
		original := namespaced[len(prefix):]
		// If allowedTools is set, the original must be in it.
		if upCfg.AllowedTools != nil {
			if !contains(upCfg.AllowedTools, original) {
				return nil, false
			}
		}
		up, ok := r.upstreams[upName]
		if !ok {
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

// List returns every namespaced tool exposed by this router.
// Honours the allowedTools filter: when set, only those names are exposed;
// when nil, every tool the upstream advertises is exposed.
func (r *Router) List() []string {
	var out []string
	for upName, upCfg := range r.cfg.Upstreams {
		if upCfg.AllowedTools != nil {
			for _, t := range upCfg.AllowedTools {
				out = append(out, Namespace(upName, t))
			}
			continue
		}
		// No allowlist: ask the upstream for its tools.
		up, ok := r.upstreams[upName]
		if !ok {
			continue
		}
		for _, t := range up.ListTools() {
			out = append(out, Namespace(upName, t))
		}
	}
	return out
}

// Upstreams returns the underlying upstream map (used by graceful shutdown).
func (r *Router) Upstreams() map[string]Upstream { return r.upstreams }

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
