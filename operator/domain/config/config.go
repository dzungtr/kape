// Package config holds operator-level platform configuration.
package config

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// KapeConfig holds cluster-wide defaults read from the kape-config ConfigMap in kape-system.
// All fields have defaults so the operator works on a fresh cluster without pre-existing config.
type KapeConfig struct {
	ClusterName string

	// Handler runtime image
	HandlerImage        string
	HandlerImageVersion string

	// kapetool sidecar image (retained for backward compat; not injected after slice 5)
	KapetoolImage        string
	KapetoolImageVersion string

	// kapeproxy sidecar image
	KapeproxyImage        string
	KapeproxyImageVersion string

	// NATS monitoring endpoint for KEDA ScaledObject
	NATSMonitoringEndpoint string

	// NATSStreamName is the JetStream stream KEDA scales the handler against.
	NATSStreamName string

	// Qdrant vector database
	QdrantVersion      string
	QdrantStorageClass string

	// Default max iterations for the ReAct loop (overridable per KapeHandler)
	DefaultMaxIterations int32
}

// HandlerImageRef returns the full image reference (image:version) for the handler container.
func (c KapeConfig) HandlerImageRef() string {
	img := c.HandlerImage
	if img == "" {
		img = "kape/handler"
	}
	ver := c.HandlerImageVersion
	if ver == "" {
		ver = "latest"
	}
	return fmt.Sprintf("%s:%s", img, ver)
}

// KapetoolImageRef returns the full image reference for the kapetool sidecar.
func (c KapeConfig) KapetoolImageRef() string {
	img := c.KapetoolImage
	if img == "" {
		img = "kape/kapetool"
	}
	ver := c.KapetoolImageVersion
	if ver == "" {
		ver = "latest"
	}
	return fmt.Sprintf("%s:%s", img, ver)
}

// KapeproxyImageRef returns the full image reference for the kapeproxy sidecar.
func (c KapeConfig) KapeproxyImageRef() string {
	img := c.KapeproxyImage
	if img == "" {
		img = "kape/kapeproxy"
	}
	ver := c.KapeproxyImageVersion
	if ver == "" {
		ver = "latest"
	}
	return fmt.Sprintf("%s:%s", img, ver)
}

// WithDefaults returns a copy of KapeConfig with default values applied where fields are zero.
func (c KapeConfig) WithDefaults() KapeConfig {
	if c.ClusterName == "" {
		c.ClusterName = "default"
	}
	if c.HandlerImage == "" {
		c.HandlerImage = "kape/handler"
	}
	if c.HandlerImageVersion == "" {
		c.HandlerImageVersion = "latest"
	}
	if c.KapetoolImage == "" {
		c.KapetoolImage = "kape/kapetool"
	}
	if c.KapetoolImageVersion == "" {
		c.KapetoolImageVersion = "latest"
	}
	if c.KapeproxyImage == "" {
		c.KapeproxyImage = "kape/kapeproxy"
	}
	if c.KapeproxyImageVersion == "" {
		c.KapeproxyImageVersion = "latest"
	}
	if c.NATSMonitoringEndpoint == "" {
		c.NATSMonitoringEndpoint = "http://nats.kape-system:8222"
	}
	if c.NATSStreamName == "" {
		c.NATSStreamName = "kape-events"
	}
	if c.QdrantVersion == "" {
		c.QdrantVersion = "v1.14.0"
	}
	if c.QdrantStorageClass == "" {
		c.QdrantStorageClass = "standard"
	}
	if c.DefaultMaxIterations == 0 {
		c.DefaultMaxIterations = 50
	}
	return c
}

// ControllerWiring is the cluster-level env the operator writes into every
// controller Deployment it renders. It is deliberately NOT part of a
// KapeAgentServer: the CR declares the project's serving environment, while
// the gateway address, mTLS credentials dir, default provider, data-plane
// transport, and event-store path are cluster plumbing shared by every
// controller in the cluster (D5, spec #261).
//
// The values come from the operator Deployment's own environment (chart values
// controller.gatewayAddr etc. → KAPE_OPERATOR_CONTROLLER_*). An unset value is
// never defaulted: the reconciler reports it in the KapeAgentServer Ready
// condition instead of materialising a controller that cannot reach the
// gateway.
type ControllerWiring struct {
	GatewayAddr     string // KAPE_GATEWAY_ADDR — in-cluster OpenShell gateway host:port
	CredsDir        string // KAPE_CREDS_DIR — mTLS creds mount path
	CredsSecret     string // KAPE_CREDS_SECRET — Secret mounted read-only at CredsDir
	DefaultProvider string // KAPE_DEFAULT_PROVIDER — provider attached to created sandboxes
	Transport       string // KAPE_TRANSPORT — pull (default data plane) or exec
	EventStore      string // KAPE_EVENT_STORE — durable event log path

	// ExtraEnv and ExtraEnvFrom are chart-owned feature wiring passed through to
	// the rendered controller verbatim (e.g. KAPE_MONITOR_*, KAPE_REVIVE_*).
	// The operator knows no feature names; the chart decides what rides here.
	// Entries that would shadow the CR-derived env contract are dropped — see
	// IsContractEnv.
	ExtraEnv     []corev1.EnvVar
	ExtraEnvFrom []corev1.EnvFromSource
}

// IsContractEnv reports whether name is owned by the CR-derived env contract
// (spec #261 M2). Wiring env may never shadow these: the KapeAgentServer is the
// single source of truth for them.
func IsContractEnv(name string) bool {
	switch name {
	case "KAPE_DEFAULT_IMAGE", "KAPE_DEFAULT_MODEL", "KAPE_DEFAULT_RESOURCES",
		"KAPE_MODEL_GATEWAY_URL", "KAPE_SERVER_DESCRIPTION", "KAPE_CREDENTIAL_BINDINGS":
		return true
	}
	return false
}

// Missing returns the names of the wiring settings left unset, in a stable
// order. Empty means the wiring is complete.
func (w ControllerWiring) Missing() []string {
	checks := []struct {
		env   string
		value string
	}{
		{"KAPE_GATEWAY_ADDR", w.GatewayAddr},
		{"KAPE_CREDS_DIR", w.CredsDir},
		{"KAPE_CREDS_SECRET", w.CredsSecret},
		{"KAPE_DEFAULT_PROVIDER", w.DefaultProvider},
		{"KAPE_TRANSPORT", w.Transport},
		{"KAPE_EVENT_STORE", w.EventStore},
	}
	var missing []string
	for _, c := range checks {
		if c.value == "" {
			missing = append(missing, c.env)
		}
	}
	return missing
}
