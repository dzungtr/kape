package main

import (
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"
)

// chartOperatorDeployment is the template that sets the operator's env vars.
const chartOperatorDeployment = "../../charts/kape/templates/operator/deployment.yaml"

// envNameForFlag mirrors ff's flag→env derivation (ff@v3: upper-case the flag
// name, replace '-', '.' and '/' with '_', prepend the upper-cased prefix).
// TestEveryFlagBindsToDerivedEnvName proves the mirror against ff itself, so a
// chart var spelled from this helper cannot silently disagree with the parser.
func envNameForFlag(name string) string {
	replacer := strings.NewReplacer("-", "_", ".", "_", "/", "_")
	return strings.ToUpper(operatorEnvVarPrefix) + "_" + replacer.Replace(strings.ToUpper(name))
}

var integerDefault = regexp.MustCompile(`^-?[0-9]+$`)

// probeValueFor returns a value that differs from the flag's default, so a
// successful parse proves the env var actually reached the flag.
func probeValueFor(defValue string) string {
	switch defValue {
	case "true":
		return "false"
	case "false":
		return "true"
	}
	if integerDefault.MatchString(defValue) {
		if defValue == "7" {
			return "8"
		}
		return "7"
	}
	return "k265-env-probe"
}

// TestEveryFlagBindsToDerivedEnvName pins the flag→env-var contract the chart
// must honour: for every registered flag, setting the derived env name changes
// the parsed value. This is the mechanical guard for #265 — the chart shipped
// a misspelled env name (one extra H), which bound to no flag at all.
func TestEveryFlagBindsToDerivedEnvName(t *testing.T) {
	flags := newFlagSet(&config{})
	flags.VisitAll(func(f *flag.Flag) {
		t.Run(f.Name, func(t *testing.T) {
			probe := probeValueFor(f.DefValue)
			t.Setenv(envNameForFlag(f.Name), probe)

			cfg := &config{}
			fs := newFlagSet(cfg)
			if err := parseConfig(fs, nil); err != nil {
				t.Fatalf("parsing config with %s=%q: %v", envNameForFlag(f.Name), probe, err)
			}
			if got := fs.Lookup(f.Name).Value.String(); got != probe {
				t.Fatalf("%s=%q did not bind to flag %q: got %q", envNameForFlag(f.Name), probe, f.Name, got)
			}
		})
	})
}

// TestOpenShellEnvVarsReachConfig is the feature-level half of the same pin:
// the three names the chart renders for the credential writer must land in the
// config fields the writer is gated on.
func TestOpenShellEnvVarsReachConfig(t *testing.T) {
	t.Setenv("KAPE_OPERATOR_OPENSHELL_GATEWAY_ADDR", "openshell.k265.svc:8080")
	t.Setenv("KAPE_OPERATOR_OPENSHELL_CREDS_DIR", "/var/run/kape-operator/openshell-mtls")
	t.Setenv("KAPE_OPERATOR_OPENSHELL_WORKSPACE", "k265-workspace")

	cfg := &config{}
	if err := parseConfig(newFlagSet(cfg), nil); err != nil {
		t.Fatalf("parsing config: %v", err)
	}
	if cfg.OpenShellGatewayAddr != "openshell.k265.svc:8080" {
		t.Errorf("OpenShellGatewayAddr = %q", cfg.OpenShellGatewayAddr)
	}
	if cfg.OpenShellCredsDir != "/var/run/kape-operator/openshell-mtls" {
		t.Errorf("OpenShellCredsDir = %q", cfg.OpenShellCredsDir)
	}
	if cfg.OpenShellWorkspace != "k265-workspace" {
		t.Errorf("OpenShellWorkspace = %q", cfg.OpenShellWorkspace)
	}
}

// TestChartOperatorEnvVarsMatchFlags asserts every KAPE_OPERATOR_* var the chart
// sets is the name ff derives from a real flag, and that the credential-writer
// trio is still rendered. Expectations are derived from newFlagSet, so a chart
// typo or a flag rename fails here instead of silently disabling a feature.
func TestChartOperatorEnvVarsMatchFlags(t *testing.T) {
	raw, err := os.ReadFile(chartOperatorDeployment)
	if err != nil {
		t.Fatalf("reading chart template: %v", err)
	}

	known := map[string]bool{}
	newFlagSet(&config{}).VisitAll(func(f *flag.Flag) {
		known[envNameForFlag(f.Name)] = true
	})

	chartVars := regexp.MustCompile(`- name: (KAPE_OPERATOR_[A-Z0-9_]+)`).FindAllStringSubmatch(string(raw), -1)
	if len(chartVars) == 0 {
		t.Fatalf("no KAPE_OPERATOR_* env vars found in %s", chartOperatorDeployment)
	}
	seen := map[string]bool{}
	for _, match := range chartVars {
		name := match[1]
		seen[name] = true
		if !known[name] {
			t.Errorf("chart sets %s, which matches no operator flag (ff would ignore it)", name)
		}
	}

	for _, flagName := range []string{"openshell-gateway-addr", "openshell-creds-dir", "openshell-workspace"} {
		if want := envNameForFlag(flagName); !seen[want] {
			t.Errorf("chart does not set %s (flag --%s); the credential writer stays disabled", want, flagName)
		}
	}
}
