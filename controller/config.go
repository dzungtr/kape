package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
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
//
// Provider is intentionally NOT a caller field (issue #220, option B):
// inference routing is deployment config — every sandbox attaches
// Config.DefaultProvider. Callers discover valid providers via
// list_providers / GET /providers.
type CreateProfile struct {
	Name      string     `json:"name"`      // sandbox name (DNS label); default "sbx-<id>"; never inherited from the parent (names must be unique)
	Image     string     `json:"image"`     // OCI image reference; default: parent's resolved image (child), else Config.DefaultImage
	Resources *Resources `json:"resources"` // cpu/memory quantities; default: parent's resolved resources (child), else Config.DefaultResources
	Model     string     `json:"model"`     // model id patched into pi models.json + PI_MODEL; default: parent's resolved model (child), else image's own selection
	// ParentID (issue #216, ADR-0083 D8): optional parent agent for spawning.
	// The child inherits the parent's profile unless overridden, and is
	// subject to the spawn caps (Config.SpawnMaxDepth / SpawnMaxChildren).
	ParentID string `json:"parent_id,omitempty"`
}

// Validation rules (consumed by the MCP create tool and smoke tests):
//   - name:        optional; DNS-1123 label, lowercase alphanumerics and '-',
//     ≤19 chars (OpenShell gateway sandbox-name cap), start/end alphanumeric
//   - image:       optional; OCI-style reference with optional tag and
//     sha256 digest, no whitespace or uppercase-host nonsense
//   - resources:   optional; cpu/memory as Kubernetes quantities
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
	if p.Name != "" && (len(p.Name) > 19 || !agentNameRe.MatchString(p.Name)) {
		return fieldErr("name", fmt.Sprintf("%q must be a lowercase DNS label (alphanumerics and '-', ≤19 chars — OpenShell gateway cap)", p.Name))
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
	if p.Model != "" && !modelRe.MatchString(p.Model) {
		return fieldErr("model", fmt.Sprintf("%q must contain only [A-Za-z0-9._:/-]", p.Model))
	}
	if p.ParentID != "" && !providerRe.MatchString(p.ParentID) {
		return fieldErr("parent_id", fmt.Sprintf("%q must be a non-empty agent id", p.ParentID))
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
		Provider:  c.DefaultProvider, // never caller-supplied (issue #220, option B)
		Model:     p.Model,
		Resources: c.DefaultResources,
	}
	if r.Name == "" {
		r.Name = "" // caller (AgentManager.Create) supplies "sbx-<id>"
	}
	if r.Image == "" {
		r.Image = c.DefaultImage
	}
	if p.Resources != nil {
		r.Resources = *p.Resources
	}
	// Re-validate the resolved values: bad config defaults surface with the
	// same field-naming contract as bad request overrides.
	resolved := &CreateProfile{Name: r.Name, Image: r.Image, Model: r.Model, Resources: &r.Resources}
	if err := resolved.Validate(); err != nil {
		return ResolvedProfile{}, err
	}
	return r, nil
}

// Data-plane transports (issue #212, ADR-0083 D1/D2). TransportPull is the
// data plane: a per-agent puller long-polls the in-sandbox daemon.
// TransportExec is the demoted exec-stream pump — break-glass ops only
// (issue #153), kept for the live-cluster smoke path until the daemon ships
// in real sandbox images.
const (
	TransportPull = "pull"
	TransportExec = "exec"
)

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
	EventStorePath   string    // durable event log file (controller-local volume)
	Transport        string    // data plane: pull (default) or exec (break-glass)
	DaemonToken      string    // static bearer token placeholder (ADR-0083 D5)
	DaemonCommand    string    // space-separated canonical main process argv threaded as SandboxSpec.command (issue #247, spec #245 E2); empty disables
	DaemonPort       string    // fixed in-sandbox daemon port (D7; default 9090)
	SandboxNamespace string    // namespace the Sandbox CRs live in (D7 resolution)

	// Spawning (issue #216).
	SpawnMaxDepth    int // max spawning depth: root=0, subagent=1, sub-subagent=2
	SpawnMaxChildren int // max children per parent agent

	ControllerServiceHost string // controller in-cluster service FQDN (D2/D7 deny rule)
	ControllerServicePort int    // controller in-cluster service port (D2/D7 deny rule)

	// SLM monitor (issue #231, spec #230). Empty APIKey disables the monitor
	// entirely (single log line, everything else unaffected).
	Monitor MonitorConfig
}

// MonitorConfig exposes the monitor knobs (issue #230, Q2/Q5/Q11):
// interval, model IDs, base URL, credentials, per-call timeout. The base
// URL is any OpenRouter-compatible endpoint — a local gateway, proxy, or
// alternative provider can serve the pipeline; swapping endpoints touches
// config only, never code.
type MonitorConfig struct {
	Interval        time.Duration // per-running-agent tick period (default 60s)
	SummarizerModel string        // summarizer model id (default scaledown/summarize)
	ClassifierModel string        // decision-modality classifier id (default scaledown/classify-1)
	BaseURL         string        // OpenRouter-compatible API base (default https://openrouter.ai/api/v1)
	APIKey          string        // OpenRouter API key (from K8s Secret); empty disables the monitor
	Timeout         time.Duration // per-call timeout (default 5s)
}

// Enabled reports whether the monitor is active: a missing or empty API key
// disables it entirely (issue #230, story 11).
func (m MonitorConfig) Enabled() bool { return m.APIKey != "" }

// DefaultConfig is the compiled-in baseline (matches the spike's flags).
func DefaultConfig() Config {
	return Config{
		GatewayAddr:           "127.0.0.1:32353",
		CredsDir:              os.Getenv("HOME") + "/.config/openshell/gateways/k8s/mtls",
		ListenAddr:            ":8081",
		DefaultProvider:       "openrouter-spike",
		DefaultImage:          "ghcr.io/dzungtr/pi-openshell:openrouter",
		DefaultResources:      Resources{CPU: "1", Memory: "2Gi"},
		ModelGatewayURL:       "http://model-gateway-http.aperture.svc.cluster.local/v1",
		EventStorePath:        "controller-events.db",
		Transport:             TransportPull,
		DaemonCommand:         "kape-daemon", // E2 default; empty = profile-hook fallback (#217)
		DaemonPort:            "9090", // fixed daemon port (D7)
		SandboxNamespace:      "openshell",
		ControllerServiceHost: "kape-controller.openshell.svc.cluster.local",
		ControllerServicePort: 8081,
		SpawnMaxDepth:         2, // D8: root → subagent → sub-subagent
		SpawnMaxChildren:      8,
		Monitor: MonitorConfig{
			Interval:        60 * time.Second, // spec #230 Q2 default
			SummarizerModel: "scaledown/summarize",
			ClassifierModel: "scaledown/classify-1",
			BaseURL:         "https://openrouter.ai/api/v1",
			APIKey:          "",              // empty key → monitor disabled (single log line)
			Timeout:         5 * time.Second, // per-call (spec #230 Q10)
		},
	}
}

// envConfig applies KAPE_* environment overrides on top of def. A malformed
// numeric override is returned as an error instead of being silently
// swallowed (review round 1, P2).
func envConfig(def Config) (Config, error) {
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
	if v := os.Getenv("KAPE_EVENT_STORE"); v != "" {
		def.EventStorePath = v
	}
	if v := os.Getenv("KAPE_TRANSPORT"); v != "" {
		def.Transport = v
	}
	if v := os.Getenv("KAPE_DAEMON_PORT"); v != "" {
		def.DaemonPort = v
	}
	if v := os.Getenv("KAPE_SANDBOX_NAMESPACE"); v != "" {
		def.SandboxNamespace = v
	}
	if v := os.Getenv("KAPE_CONTROLLER_SERVICE_HOST"); v != "" {
		def.ControllerServiceHost = v
	}
	if v := os.Getenv("KAPE_CONTROLLER_SERVICE_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return def, fmt.Errorf("KAPE_CONTROLLER_SERVICE_PORT %q is not an integer: %w", v, err)
		}
		def.ControllerServicePort = p
	}
	if v := os.Getenv("KAPE_DAEMON_TOKEN"); v != "" {
		def.DaemonToken = v
	}
	if v := os.Getenv("KAPE_DAEMON_COMMAND"); v != "" {
		def.DaemonCommand = v
	}
	if v := os.Getenv("KAPE_SPAWN_MAX_DEPTH"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return def, fmt.Errorf("KAPE_SPAWN_MAX_DEPTH %q must be a non-negative integer", v)
		}
		def.SpawnMaxDepth = n
	}
	if v := os.Getenv("KAPE_SPAWN_MAX_CHILDREN"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return def, fmt.Errorf("KAPE_SPAWN_MAX_CHILDREN %q must be a positive integer", v)
		}
		def.SpawnMaxChildren = n
	}
	if v := os.Getenv("KAPE_MONITOR_INTERVAL_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return def, fmt.Errorf("KAPE_MONITOR_INTERVAL_SECONDS %q must be a positive integer", v)
		}
		def.Monitor.Interval = time.Duration(n) * time.Second
	}
	if v := os.Getenv("KAPE_MONITOR_SUMMARIZER_MODEL"); v != "" {
		def.Monitor.SummarizerModel = v
	}
	if v := os.Getenv("KAPE_MONITOR_CLASSIFIER_MODEL"); v != "" {
		def.Monitor.ClassifierModel = v
	}
	if v := os.Getenv("KAPE_MONITOR_BASE_URL"); v != "" {
		def.Monitor.BaseURL = v
	}
	if v := os.Getenv("KAPE_MONITOR_OPENROUTER_API_KEY"); v != "" {
		def.Monitor.APIKey = v
	}
	if v := os.Getenv("KAPE_MONITOR_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return def, fmt.Errorf("KAPE_MONITOR_TIMEOUT_SECONDS %q must be a positive integer", v)
		}
		def.Monitor.Timeout = time.Duration(n) * time.Second
	}
	return def, nil
}

// ConfigFromEnv resolves the full config: compiled defaults, then KAPE_* env.
func ConfigFromEnv() (Config, error) { return envConfig(DefaultConfig()) }

// DaemonCommandArgv splits DaemonCommand into the argv threaded as
// SandboxSpec.command (issue #247, spec #245 E2). Empty config → nil, which
// keeps the create request without a command (gateway default main process,
// the #217 profile-hook fallback posture).
func (c Config) DaemonCommandArgv() []string {
	if c.DaemonCommand == "" {
		return nil
	}
	return strings.Fields(c.DaemonCommand)
}

// RegisterFlags adds flag overrides (local dev) onto fs. Flags win over env.
func (c *Config) RegisterFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.GatewayAddr, "gateway", c.GatewayAddr, "OpenShell gateway host:port (env KAPE_GATEWAY_ADDR)")
	fs.StringVar(&c.CredsDir, "creds", c.CredsDir, "mTLS creds dir (env KAPE_CREDS_DIR)")
	fs.StringVar(&c.ListenAddr, "listen", c.ListenAddr, "HTTP listen address (env KAPE_LISTEN_ADDR)")
	fs.StringVar(&c.DefaultProvider, "provider", c.DefaultProvider, "default OpenShell provider (env KAPE_DEFAULT_PROVIDER)")
	fs.StringVar(&c.DefaultImage, "image", c.DefaultImage, "default sandbox OCI image (env KAPE_DEFAULT_IMAGE)")
	fs.StringVar(&c.ModelGatewayURL, "model-gateway-url", c.ModelGatewayURL, "model-gateway base URL patched into pi models.json (env KAPE_MODEL_GATEWAY_URL)")
	fs.StringVar(&c.EventStorePath, "event-store", c.EventStorePath, "durable event log file (env KAPE_EVENT_STORE)")
	fs.StringVar(&c.Transport, "transport", c.Transport, "data plane: pull (default) or exec break-glass (env KAPE_TRANSPORT)")
	fs.StringVar(&c.DaemonPort, "daemon-port", c.DaemonPort, "fixed in-sandbox daemon port (env KAPE_DAEMON_PORT)")
	fs.StringVar(&c.SandboxNamespace, "sandbox-namespace", c.SandboxNamespace, "namespace holding the Sandbox CRs (env KAPE_SANDBOX_NAMESPACE)")
	fs.StringVar(&c.ControllerServiceHost, "controller-service-host", c.ControllerServiceHost, "controller in-cluster service FQDN for the egress deny rule (env KAPE_CONTROLLER_SERVICE_HOST)")
	fs.IntVar(&c.ControllerServicePort, "controller-service-port", c.ControllerServicePort, "controller in-cluster service port for the egress deny rule (env KAPE_CONTROLLER_SERVICE_PORT)")
	fs.StringVar(&c.DaemonToken, "daemon-token", c.DaemonToken, "daemon bearer token placeholder (env KAPE_DAEMON_TOKEN)")
	fs.StringVar(&c.DaemonCommand, "daemon-command", c.DaemonCommand, "space-separated canonical main process argv (env KAPE_DAEMON_COMMAND); empty disables")
	fs.IntVar(&c.SpawnMaxDepth, "spawn-max-depth", c.SpawnMaxDepth, "max spawning depth, root=0 (env KAPE_SPAWN_MAX_DEPTH)")
	fs.IntVar(&c.SpawnMaxChildren, "spawn-max-children", c.SpawnMaxChildren, "max children per parent agent (env KAPE_SPAWN_MAX_CHILDREN)")
	fs.Func("resources", "default resources JSON {\"cpu\":...,\"memory\":...} (env KAPE_DEFAULT_RESOURCES)", func(v string) error {
		var r Resources
		if err := json.Unmarshal([]byte(v), &r); err != nil {
			return fmt.Errorf("resources: %v", err)
		}
		c.DefaultResources = r
		return nil
	})
	fs.Func("monitor-interval-seconds", "monitor tick period for running agents (env KAPE_MONITOR_INTERVAL_SECONDS)", func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("monitor-interval-seconds %q must be a positive integer", v)
		}
		c.Monitor.Interval = time.Duration(n) * time.Second
		return nil
	})
	fs.StringVar(&c.Monitor.SummarizerModel, "monitor-summarizer-model", c.Monitor.SummarizerModel, "summarizer model id (env KAPE_MONITOR_SUMMARIZER_MODEL)")
	fs.StringVar(&c.Monitor.ClassifierModel, "monitor-classifier-model", c.Monitor.ClassifierModel, "decision-modality classifier id (env KAPE_MONITOR_CLASSIFIER_MODEL)")
	fs.StringVar(&c.Monitor.BaseURL, "monitor-base-url", c.Monitor.BaseURL, "OpenRouter-compatible API base URL (env KAPE_MONITOR_BASE_URL)")
	fs.StringVar(&c.Monitor.APIKey, "monitor-openrouter-api-key", c.Monitor.APIKey, "OpenRouter API key; empty disables the monitor (env KAPE_MONITOR_OPENROUTER_API_KEY)")
	fs.Func("monitor-timeout-seconds", "SLM per-call timeout (env KAPE_MONITOR_TIMEOUT_SECONDS)", func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("monitor-timeout-seconds %q must be a positive integer", v)
		}
		c.Monitor.Timeout = time.Duration(n) * time.Second
		return nil
	})
}

// Validate checks the resolved config itself (flag/env mistakes surface at
// startup, not at first create).
func (c *Config) Validate() error {
	if c.DefaultProvider == "" || !providerRe.MatchString(c.DefaultProvider) {
		return fieldErr("provider", fmt.Sprintf("%q must be a non-empty provider name ([A-Za-z0-9._-]) — must match a gateway provider profile (list_providers)", c.DefaultProvider))
	}
	p := &CreateProfile{Image: c.DefaultImage, Resources: &c.DefaultResources}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if c.ModelGatewayURL == "" || !strings.HasPrefix(c.ModelGatewayURL, "http") {
		return fieldErr("model-gateway-url", "must be an http(s) base URL")
	}
	if c.GatewayAddr == "" || c.ListenAddr == "" {
		return fieldErr("config", "gateway address and listen address are required")
	}
	if c.EventStorePath == "" {
		return fieldErr("event-store", "durable event log path is required")
	}
	if c.Monitor.Enabled() {
		if c.Monitor.BaseURL == "" || !strings.HasPrefix(c.Monitor.BaseURL, "http") {
			return fieldErr("monitor-base-url", "must be an http(s) base URL")
		}
		if c.Monitor.Interval <= 0 || c.Monitor.Timeout <= 0 {
			return fieldErr("monitor", "interval and per-call timeout must be positive")
		}
	}
	switch c.Transport {
	case TransportPull:
		// Daemon addressing comes from the Sandbox CR (D7, no static base URL).
		if port, err := strconv.Atoi(c.DaemonPort); err != nil || port < 1 || port > 65535 {
			return fieldErr("daemon-port", fmt.Sprintf("%q must be a TCP port number", c.DaemonPort))
		}
		if c.SandboxNamespace == "" {
			return fieldErr("sandbox-namespace", "required when transport is pull (Sandbox CR resolution)")
		}
		if c.ControllerServiceHost == "" {
			return fieldErr("controller-service-host", "required when transport is pull (sandbox→controller egress deny rule)")
		}
		if c.ControllerServicePort < 1 || c.ControllerServicePort > 65535 {
			return fieldErr("controller-service-port", fmt.Sprintf("%d must be a TCP port number", c.ControllerServicePort))
		}
	case TransportExec:
		// break-glass exec path: no daemon config required.
	default:
		return fieldErr("transport", fmt.Sprintf("must be %q or %q", TransportPull, TransportExec))
	}
	return nil
}
