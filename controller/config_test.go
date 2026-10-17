package main

import (
	"errors"
	"flag"
	"testing"
	"time"
)

// newTestFlagSet isolates flag parsing from the global CommandLine.
func newTestFlagSet() *flag.FlagSet { return flag.NewFlagSet("test", flag.ContinueOnError) }

// testConfig is the deterministic config used by FSM/handler tests.
func testConfig() Config {
	cfg := DefaultConfig()
	cfg.DefaultImage = "pi-image"
	cfg.DefaultProvider = "openrouter-spike"
	// The exec-pump tests exercise the demoted break-glass transport path
	// (issue #212); pull-transport tests use pullTestConfig in puller_test.
	cfg.Transport = TransportExec
	return cfg
}

func TestConfigFromEnvOverridesDefaults(t *testing.T) {
	t.Setenv("KAPE_DEFAULT_IMAGE", "env-image")
	t.Setenv("KAPE_DEFAULT_PROVIDER", "env-provider")
	t.Setenv("KAPE_LISTEN_ADDR", ":9999")
	t.Setenv("KAPE_DEFAULT_RESOURCES", `{"cpu":"2","memory":"4Gi"}`)

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.DefaultImage != "env-image" || cfg.DefaultProvider != "env-provider" || cfg.ListenAddr != ":9999" {
		t.Fatalf("env not applied: %+v", cfg)
	}
	if cfg.DefaultResources != (Resources{CPU: "2", Memory: "4Gi"}) {
		t.Fatalf("env resources = %+v", cfg.DefaultResources)
	}
}

// Issue #247 (spec #245 E2): the daemon command config is a space-separated
// argv; empty disables canonical-main-process threading (profile-hook
// fallback posture).
func TestDaemonCommandArgv(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.DaemonCommandArgv(); len(got) != 1 || got[0] != "kape-daemon" {
		t.Fatalf("default DaemonCommandArgv = %v, want [kape-daemon]", got)
	}
	cfg.DaemonCommand = "kape-daemon --spool /s/x.jsonl"
	got := cfg.DaemonCommandArgv()
	if len(got) != 3 || got[0] != "kape-daemon" || got[1] != "--spool" || got[2] != "/s/x.jsonl" {
		t.Fatalf("DaemonCommandArgv = %v", got)
	}
	cfg.DaemonCommand = ""
	if got := cfg.DaemonCommandArgv(); got != nil {
		t.Fatalf("empty DaemonCommand = %v, want nil (disabled)", got)
	}
}

// A malformed KAPE_CONTROLLER_SERVICE_PORT must surface as an error, not be
// silently swallowed (review round 1, P2).
func TestConfigFromEnvRejectsGarbageControllerPort(t *testing.T) {
	t.Setenv("KAPE_CONTROLLER_SERVICE_PORT", "not-a-number")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("ConfigFromEnv accepted garbage KAPE_CONTROLLER_SERVICE_PORT, want error")
	}
}

func TestFlagOverridesEnv(t *testing.T) {
	t.Setenv("KAPE_DEFAULT_IMAGE", "env-image")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	fs := newTestFlagSet()
	cfg.RegisterFlags(fs)
	if err := fs.Parse([]string{"-image", "flag-image"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if cfg.DefaultImage != "flag-image" {
		t.Fatalf("image = %q, want flag to beat env", cfg.DefaultImage)
	}
}

func TestConfigValidateRejectsBadDefaults(t *testing.T) {
	cfg := testConfig()
	cfg.DefaultImage = "bad image ref"
	err := cfg.Validate()
	if err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want validation error", err)
	}
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "image" {
		t.Fatalf("err = %v, want field=image", err)
	}
}

func TestResolveProfileDefaultsFillOmittedFields(t *testing.T) {
	cfg := testConfig()
	r, err := cfg.ResolveProfile(nil)
	if err != nil {
		t.Fatalf("ResolveProfile(nil): %v", err)
	}
	if r.Image != "pi-image" || r.Provider != "openrouter-spike" || r.Resources.CPU != "1" || r.Resources.Memory != "2Gi" {
		t.Fatalf("defaults not applied: %+v", r)
	}
	if r.Model != "" {
		t.Fatalf("model default = %q, want empty (image default selection)", r.Model)
	}
}

func TestResolveProfileOverridesWin(t *testing.T) {
	cfg := testConfig()
	r, err := cfg.ResolveProfile(&CreateProfile{
		Image:     "other-image:v2",
		Resources: &Resources{CPU: "4", Memory: "8Gi"},
		Model:     "z-ai/glm-5.2",
	})
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	// Provider is deployment-fixed: overrides cannot change it (issue #220).
	if r.Image != "other-image:v2" || r.Provider != "openrouter-spike" || r.Model != "z-ai/glm-5.2" {
		t.Fatalf("overrides not applied: %+v", r)
	}
	if r.Resources != (Resources{CPU: "4", Memory: "8Gi"}) {
		t.Fatalf("resources = %+v", r.Resources)
	}
}

func TestResolveProfileFieldNamedErrors(t *testing.T) {
	cases := []struct {
		name  string
		prof  CreateProfile
		field string
	}{
		{"bad name", CreateProfile{Name: "Bad_Name"}, "name"},
		{"bad image", CreateProfile{Image: "not an image"}, "image"},
		{"bad cpu", CreateProfile{Resources: &Resources{CPU: "lots", Memory: "2Gi"}}, "resources"},
		{"bad memory", CreateProfile{Resources: &Resources{CPU: "1", Memory: "twoGi"}}, "resources"},
		{"bad model", CreateProfile{Model: "model with space"}, "model"},
	}
	cfg := testConfig()
	for _, tc := range cases {
		_, err := cfg.ResolveProfile(&tc.prof)
		if err == nil {
			t.Fatalf("%s: accepted, want rejection", tc.name)
		}
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("%s: err = %v, want ErrValidation", tc.name, err)
		}
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != tc.field {
			t.Fatalf("%s: err = %v, want field=%s", tc.name, err, tc.field)
		}
	}
}

// ---- Monitor config (issue #231) ----

func TestMonitorConfigDefaults(t *testing.T) {
	m := DefaultConfig().Monitor
	if m.Interval != 60*time.Second {
		t.Fatalf("interval = %v, want 60s", m.Interval)
	}
	if m.SummarizerModel != "scaledown/summarize" || m.ClassifierModel != "scaledown/classify-1" {
		t.Fatalf("model ids = %q / %q", m.SummarizerModel, m.ClassifierModel)
	}
	if m.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("base URL = %q", m.BaseURL)
	}
	if m.Timeout != 5*time.Second {
		t.Fatalf("timeout = %v, want 5s", m.Timeout)
	}
	if m.APIKey != "" {
		t.Fatalf("APIKey default = %q, want empty", m.APIKey)
	}
}

func TestMonitorDisabledOnEmptyKey(t *testing.T) {
	var m MonitorConfig
	if m.Enabled() {
		t.Fatalf("empty key must disable the monitor")
	}
	m.APIKey = "  "
	if !m.Enabled() {
		t.Fatalf("whitespace key counts as set (disable semantics = empty string only)")
	}
	m.APIKey = ""
	if m.Enabled() {
		t.Fatalf("monitor enabled without key")
	}
}

func TestMonitorEnvOverrides(t *testing.T) {
	t.Setenv("KAPE_MONITOR_INTERVAL_SECONDS", "30")
	t.Setenv("KAPE_MONITOR_SUMMARIZER_MODEL", "fb/sum")
	t.Setenv("KAPE_MONITOR_CLASSIFIER_MODEL", "fb/cls")
	t.Setenv("KAPE_MONITOR_BASE_URL", "http://gw.internal/api/v1")
	t.Setenv("KAPE_MONITOR_OPENROUTER_API_KEY", "sk-live")
	t.Setenv("KAPE_MONITOR_TIMEOUT_SECONDS", "3")

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	m := cfg.Monitor
	if m.Interval != 30*time.Second || m.Timeout != 3*time.Second {
		t.Fatalf("durations = %v / %v", m.Interval, m.Timeout)
	}
	if m.SummarizerModel != "fb/sum" || m.ClassifierModel != "fb/cls" || m.BaseURL != "http://gw.internal/api/v1" {
		t.Fatalf("overrides not applied: %+v", m)
	}
	if !m.Enabled() {
		t.Fatalf("monitor disabled despite key")
	}
}

func TestMonitorBadEnvRejected(t *testing.T) {
	t.Setenv("KAPE_MONITOR_INTERVAL_SECONDS", "zero")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatalf("bad interval accepted")
	}
}

func TestMonitorValidateEnablesBaseURLCheck(t *testing.T) {
	cfg := testConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	cfg.Monitor.APIKey = "sk"
	cfg.Monitor.BaseURL = ""
	if err := cfg.Validate(); err == nil {
		t.Fatalf("enabled monitor with empty base URL accepted")
	}
	// Disabled monitor (empty key) never fails validation — missing key must
	// not break anything else.
	cfg.Monitor.APIKey = ""
	cfg.Monitor.BaseURL = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled monitor broke validation: %v", err)
	}
}

func TestMonitorFlagsRegistered(t *testing.T) {
	cfg := testConfig()
	fs := newTestFlagSet()
	cfg.RegisterFlags(fs)
	if err := fs.Parse([]string{"--monitor-interval-seconds", "120", "--monitor-openrouter-api-key", "sk-f", "--monitor-timeout-seconds", "7"}); err != nil {
		t.Fatalf("flag parse: %v", err)
	}
	if cfg.Monitor.Interval != 120*time.Second || cfg.Monitor.APIKey != "sk-f" || cfg.Monitor.Timeout != 7*time.Second {
		t.Fatalf("flag overrides not applied: %+v", cfg.Monitor)
	}
}
