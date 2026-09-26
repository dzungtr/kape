package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Validation sentinel + per-field error: transports map ErrValidation to 400
// and surface FieldError.Field to the caller — validation errors name the
// offending field, never a bare message.
var ErrValidation = errors.New("invalid create request")

type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func (e *FieldError) Unwrap() error { return ErrValidation }

func fieldErr(field, reason string) error { return &FieldError{Field: field, Reason: reason} }

// Resources is the sandbox resource profile: Kubernetes quantities for cpu
// and memory. It is the only structured override field; everything else is
// a plain string.
type Resources struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

var (
	cpuQuantityRe    = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?m?$`)
	memoryQuantityRe = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:E|P|T|G|M|K|Ei|Pi|Ti|Gi|Mi|Ki)?$`)
)

// UnmarshalJSON decodes {"cpu": "...", "memory": "..."} and names "resources"
// (not the inner field) as the invalid field, per the create-profile contract.
func (r *Resources) UnmarshalJSON(data []byte) error {
	type plain Resources
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return fieldErr("resources", "must be an object {\"cpu\": string, \"memory\": string}")
	}
	*r = Resources(p)
	return nil
}

func (r Resources) Validate() error {
	if !cpuQuantityRe.MatchString(r.CPU) {
		return fieldErr("resources", fmt.Sprintf("cpu %q is not a Kubernetes CPU quantity (e.g. \"1\", \"500m\")", r.CPU))
	}
	if !memoryQuantityRe.MatchString(r.Memory) {
		return fieldErr("resources", fmt.Sprintf("memory %q is not a Kubernetes memory quantity (e.g. \"2Gi\", \"512Mi\")", r.Memory))
	}
	return nil
}

// CreateProfile is the create-time override set for POST /agents /
// create_agent: every field is optional; omitted fields fall back to
// Config defaults (env, then flag override).
type CreateProfile struct {
	Name      string     `json:"name"`      // sandbox name (DNS label); default "sbx-<id>"
	Image     string     `json:"image"`     // OCI image reference; default Config.DefaultImage
	Resources *Resources `json:"resources"` // cpu/memory quantities; default Config.DefaultResources
	Provider  string     `json:"provider"`  // OpenShell provider name; default Config.DefaultProvider
	Model     string     `json:"model"`     // model id patched into pi models.json + PI_MODEL; default: image's own selection
}

// Validation rules (consumed by the MCP create tool and smoke tests):
//   - name:        optional; DNS-1123 label, lowercase alphanumerics and '-',
//     ≤63 chars, start/end alphanumeric
//   - image:       optional; OCI-style reference with optional tag and
//     sha256 digest, no whitespace or uppercase-host nonsense
//   - resources:   optional; cpu/memory as Kubernetes quantities
//   - provider:    optional; non-empty, [A-Za-z0-9._-] only
//   - model:       optional; non-empty, [A-Za-z0-9._:/-] only (provider-
//     qualified ids like "openrouter/z-ai/glm-5.2" allowed)
var (
	agentNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	imageRefRe  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*(?::[0-9]+)?(?:/[a-zA-Z0-9._/-]+)*(?::[a-zA-Z0-9._-]+)?(?:@sha256:[a-f0-9]{64})?$`)
	providerRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	modelRe     = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)
)

// Validate checks only the fields the caller supplied; it never rejects an
// empty field (that is what defaults are for). Each failure names its field.
func (p *CreateProfile) Validate() error {
	if p.Name != "" && !agentNameRe.MatchString(p.Name) {
		return fieldErr("name", fmt.Sprintf("%q must be a lowercase DNS label (alphanumerics and '-', ≤63 chars)", p.Name))
	}
	if p.Image != "" {
		if strings.ContainsAny(p.Image, " \t\n") || !imageRefRe.MatchString(p.Image) {
			return fieldErr("image", fmt.Sprintf("%q is not a valid OCI image reference", p.Image))
		}
	}
	if p.Resources != nil {
		if err := p.Resources.Validate(); err != nil {
			return err
		}
	}
	if p.Provider != "" && !providerRe.MatchString(p.Provider) {
		return fieldErr("provider", fmt.Sprintf("%q must contain only [A-Za-z0-9._-]", p.Provider))
	}
	if p.Model != "" && !modelRe.MatchString(p.Model) {
		return fieldErr("model", fmt.Sprintf("%q must contain only [A-Za-z0-9._:/-]", p.Model))
	}
	return nil
}

// ResolvedProfile is a CreateProfile with every default filled in — the value
// set handed to the gateway and the pi wrapper.
type ResolvedProfile struct {
	Name      string
	Image     string
	Resources Resources
	Provider  string
	Model     string // "" means: leave pi's image default model untouched
}

// ResolveProfile validates the requested overrides and fills omitted fields
// from config. Defaults are re-validated so a misconfigured operator gets the
// same field-naming 400 rather than a downstream gateway error.
func (c *Config) ResolveProfile(p *CreateProfile) (ResolvedProfile, error) {
	if p == nil {
		p = &CreateProfile{}
	}
	if err := p.Validate(); err != nil {
		return ResolvedProfile{}, err
	}
	r := ResolvedProfile{
		Name:      p.Name,
		Image:     p.Image,
		Provider:  p.Provider,
		Model:     p.Model,
		Resources: c.DefaultResources,
	}
	if r.Name == "" {
		r.Name = "" // caller (AgentManager.Create) supplies "sbx-<id>"
	}
	if r.Image == "" {
		r.Image = c.DefaultImage
	}
	if r.Provider == "" {
		r.Provider = c.DefaultProvider
	}
	if p.Resources != nil {
		r.Resources = *p.Resources
	}
	// Re-validate the resolved values: bad config defaults surface with the
	// same field-naming contract as bad request overrides.
	resolved := &CreateProfile{Name: r.Name, Image: r.Image, Provider: r.Provider, Model: r.Model, Resources: &r.Resources}
	if err := resolved.Validate(); err != nil {
		return ResolvedProfile{}, err
	}
	return r, nil
}

// Config is the controller runtime configuration. Defaults are compiled in,
// overridden by KAPE_* environment variables (in-cluster), then by flags
// (local dev) — flag > env > compiled default.
type Config struct {
	GatewayAddr      string    // OpenShell gateway host:port
	CredsDir         string    // mTLS creds dir (ca.crt/tls.crt/tls.key)
	ListenAddr       string    // HTTP listen address
	DefaultProvider  string    // provider attached to created sandboxes
	DefaultImage     string    // sandbox OCI image
	DefaultResources Resources // cpu/memory defaults
	ModelGatewayURL  string    // base URL patched into pi models.json
}

// DefaultConfig is the compiled-in baseline (matches the spike's flags).
func DefaultConfig() Config {
	return Config{
		GatewayAddr:      "127.0.0.1:32353",
		CredsDir:         os.Getenv("HOME") + "/.config/openshell/gateways/k8s/mtls",
		ListenAddr:       ":8081",
		DefaultProvider:  "openrouter-spike",
		DefaultImage:     "ghcr.io/dzungtr/pi-openshell:openrouter",
		DefaultResources: Resources{CPU: "1", Memory: "2Gi"},
		ModelGatewayURL:  "http://model-gateway-http.aperture.svc.cluster.local/v1",
	}
}

// envConfig applies KAPE_* environment overrides on top of def.
func envConfig(def Config) Config {
	if v := os.Getenv("KAPE_GATEWAY_ADDR"); v != "" {
		def.GatewayAddr = v
	}
	if v := os.Getenv("KAPE_CREDS_DIR"); v != "" {
		def.CredsDir = v
	}
	if v := os.Getenv("KAPE_LISTEN_ADDR"); v != "" {
		def.ListenAddr = v
	}
	if v := os.Getenv("KAPE_DEFAULT_PROVIDER"); v != "" {
		def.DefaultProvider = v
	}
	if v := os.Getenv("KAPE_DEFAULT_IMAGE"); v != "" {
		def.DefaultImage = v
	}
	if v := os.Getenv("KAPE_DEFAULT_RESOURCES"); v != "" {
		var r Resources
		if err := json.Unmarshal([]byte(v), &r); err != nil {
			panic(fmt.Sprintf("KAPE_DEFAULT_RESOURCES: %v (want {\"cpu\":...,\"memory\":...})", err))
		}
		def.DefaultResources = r
	}
	if v := os.Getenv("KAPE_MODEL_GATEWAY_URL"); v != "" {
		def.ModelGatewayURL = v
	}
	return def
}

// ConfigFromEnv resolves the full config: compiled defaults, then KAPE_* env.
func ConfigFromEnv() Config { return envConfig(DefaultConfig()) }

// RegisterFlags adds flag overrides (local dev) onto fs. Flags win over env.
func (c *Config) RegisterFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.GatewayAddr, "gateway", c.GatewayAddr, "OpenShell gateway host:port (env KAPE_GATEWAY_ADDR)")
	fs.StringVar(&c.CredsDir, "creds", c.CredsDir, "mTLS creds dir (env KAPE_CREDS_DIR)")
	fs.StringVar(&c.ListenAddr, "listen", c.ListenAddr, "HTTP listen address (env KAPE_LISTEN_ADDR)")
	fs.StringVar(&c.DefaultProvider, "provider", c.DefaultProvider, "default OpenShell provider (env KAPE_DEFAULT_PROVIDER)")
	fs.StringVar(&c.DefaultImage, "image", c.DefaultImage, "default sandbox OCI image (env KAPE_DEFAULT_IMAGE)")
	fs.StringVar(&c.ModelGatewayURL, "model-gateway-url", c.ModelGatewayURL, "model-gateway base URL patched into pi models.json (env KAPE_MODEL_GATEWAY_URL)")
	fs.Func("resources", "default resources JSON {\"cpu\":...,\"memory\":...} (env KAPE_DEFAULT_RESOURCES)", func(v string) error {
		var r Resources
		if err := json.Unmarshal([]byte(v), &r); err != nil {
			return fmt.Errorf("resources: %v", err)
		}
		c.DefaultResources = r
		return nil
	})
}

// Validate checks the resolved config itself (flag/env mistakes surface at
// startup, not at first create).
func (c *Config) Validate() error {
	p := &CreateProfile{Image: c.DefaultImage, Provider: c.DefaultProvider, Resources: &c.DefaultResources}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if c.ModelGatewayURL == "" || !strings.HasPrefix(c.ModelGatewayURL, "http") {
		return fieldErr("model-gateway-url", "must be an http(s) base URL")
	}
	if c.GatewayAddr == "" || c.ListenAddr == "" {
		return fieldErr("config", "gateway address and listen address are required")
	}
	return nil
}
