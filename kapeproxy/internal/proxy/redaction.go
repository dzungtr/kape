package proxy

import (
	"fmt"
	"strings"

	"github.com/PaesslerAG/jsonpath"
)

// Redactor applies JSONPath-driven blanking to nested maps/slices.
type Redactor struct{}

// NewRedactor returns a stateless redactor.
func NewRedactor() *Redactor { return &Redactor{} }

// Apply walks rules and blanks the matched leaves in tree IN-PLACE.
// Returns the (possibly modified) tree. Unknown paths are no-ops.
//
// Supported rule shapes (v1):
//   - $.field          (top-level scalar)
//   - $.a.b.c          (dotted nested scalar)
//   - $[N].field       (indexed array element scalar)
//
// Wildcards, slices, and predicates are NOT supported in v1.
func (r *Redactor) Apply(tree any, rules []JSONPathRule) (any, error) {
	for _, rule := range rules {
		if err := redactPath(tree, rule.JSONPath); err != nil {
			return nil, err
		}
	}
	return tree, nil
}

// redactPath sets the leaf at jsonPath to its zero value.
func redactPath(tree any, path string) error {
	if !strings.HasPrefix(path, "$") {
		return fmt.Errorf("jsonPath rule %q must start with $", path)
	}
	if strings.ContainsAny(path, "*?") {
		return fmt.Errorf("jsonPath rule %q uses unsupported feature (*, ?)", path)
	}
	// Probe: if path doesn't exist, silently skip.
	if _, err := jsonpath.Get(path, tree); err != nil {
		return nil
	}
	// Walk + overwrite.
	segs, err := splitPath(path)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return nil
	}
	parent := tree
	for i := 0; i < len(segs)-1; i++ {
		switch s := segs[i].(type) {
		case string:
			m, ok := parent.(map[string]any)
			if !ok {
				return nil
			}
			parent = m[s]
		case int:
			a, ok := parent.([]any)
			if !ok || s < 0 || s >= len(a) {
				return nil
			}
			parent = a[s]
		}
	}
	last := segs[len(segs)-1]
	switch s := last.(type) {
	case string:
		m, ok := parent.(map[string]any)
		if !ok {
			return nil
		}
		m[s] = zeroLike(m[s])
	case int:
		a, ok := parent.([]any)
		if !ok || s < 0 || s >= len(a) {
			return nil
		}
		a[s] = zeroLike(a[s])
	}
	return nil
}

// splitPath turns "$.a.b" into ["a", "b"] and "$[3].x" into [3, "x"].
func splitPath(p string) ([]any, error) {
	rest := strings.TrimPrefix(p, "$")
	var out []any
	for len(rest) > 0 {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			end := strings.IndexAny(rest, ".[")
			if end < 0 {
				out = append(out, rest)
				rest = ""
			} else {
				out = append(out, rest[:end])
				rest = rest[end:]
			}
		case '[':
			end := strings.Index(rest, "]")
			if end < 0 {
				return nil, fmt.Errorf("unmatched [ in %q", p)
			}
			idx := 0
			if _, err := fmt.Sscanf(rest[1:end], "%d", &idx); err != nil {
				return nil, fmt.Errorf("non-integer index in %q", p)
			}
			out = append(out, idx)
			rest = rest[end+1:]
		default:
			return nil, fmt.Errorf("unexpected char %q in %q", rest[0], p)
		}
	}
	return out, nil
}

// zeroLike returns the zero value of the same kind as v.
func zeroLike(v any) any {
	switch v.(type) {
	case string:
		return ""
	case bool:
		return false
	case float64, int, int64:
		return 0
	case map[string]any:
		return map[string]any{}
	case []any:
		return []any{}
	default:
		return nil
	}
}
