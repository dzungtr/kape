package proxy

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level kapeproxy-config document.
type Config struct {
	Upstreams map[string]*UpstreamConfig `yaml:"upstreams"`
}

// UpstreamConfig is one upstream MCP server entry.
type UpstreamConfig struct {
	URL          string           `yaml:"url"`
	Transport    string           `yaml:"transport"` // "sse" or "streamable-http"
	AllowedTools []string         `yaml:"allowedTools,omitempty"`
	Redaction    *RedactionConfig `yaml:"redaction,omitempty"`
	Audit        *bool            `yaml:"audit,omitempty"`
}

// RedactionConfig groups input + output redaction rules.
type RedactionConfig struct {
	Input  []JSONPathRule `yaml:"input,omitempty"`
	Output []JSONPathRule `yaml:"output,omitempty"`
}

// JSONPathRule is one JSONPath redaction directive.
type JSONPathRule struct {
	JSONPath string `yaml:"jsonPath"`
}

// LoadConfig reads + parses the kapeproxy-config YAML at path.
//
// Defaults applied during load:
//   - Audit defaults to true when omitted.
//   - AllowedTools is normalised to nil when explicitly empty (treated as "expose all").
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading kapeproxy config %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing kapeproxy config %q: %w", path, err)
	}
	for _, up := range cfg.Upstreams {
		if up.Audit == nil {
			t := true
			up.Audit = &t
		}
		if up.AllowedTools != nil && len(up.AllowedTools) == 0 {
			up.AllowedTools = nil
		}
	}
	return &cfg, nil
}
