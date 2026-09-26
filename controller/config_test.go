package main

import (
	"errors"
	"flag"
	"testing"
)

// newTestFlagSet isolates flag parsing from the global CommandLine.
func newTestFlagSet() *flag.FlagSet { return flag.NewFlagSet("test", flag.ContinueOnError) }

// testConfig is the deterministic config used by FSM/handler tests.
func testConfig() Config {
	cfg := DefaultConfig()
	cfg.DefaultImage = "pi-image"
	cfg.DefaultProvider = "openrouter-spike"
	return cfg
}

func TestConfigFromEnvOverridesDefaults(t *testing.T) {
	t.Setenv("KAPE_DEFAULT_IMAGE", "env-image")
	t.Setenv("KAPE_DEFAULT_PROVIDER", "env-provider")
	t.Setenv("KAPE_LISTEN_ADDR", ":9999")
	t.Setenv("KAPE_DEFAULT_RESOURCES", `{"cpu":"2","memory":"4Gi"}`)

	cfg := ConfigFromEnv()
	if cfg.DefaultImage != "env-image" || cfg.DefaultProvider != "env-provider" || cfg.ListenAddr != ":9999" {
		t.Fatalf("env not applied: %+v", cfg)
	}
	if cfg.DefaultResources != (Resources{CPU: "2", Memory: "4Gi"}) {
		t.Fatalf("env resources = %+v", cfg.DefaultResources)
	}
}

func TestFlagOverridesEnv(t *testing.T) {
	t.Setenv("KAPE_DEFAULT_IMAGE", "env-image")
	cfg := ConfigFromEnv()
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
		Provider:  "other-provider",
		Model:     "z-ai/glm-5.2",
	})
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if r.Image != "other-image:v2" || r.Provider != "other-provider" || r.Model != "z-ai/glm-5.2" {
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
		{"bad provider", CreateProfile{Provider: "has space"}, "provider"},
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
