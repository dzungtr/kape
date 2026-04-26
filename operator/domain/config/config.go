// Package config holds operator-level platform configuration.
package config

import "fmt"

// KapeConfig holds cluster-wide defaults read from the kape-config ConfigMap in kape-system.
// All fields have defaults so the operator works on a fresh cluster without pre-existing config.
type KapeConfig struct {
	ClusterName string

	// Handler runtime image
	HandlerImage        string
	HandlerImageVersion string

	// kapetool sidecar image
	KapetoolImage        string
	KapetoolImageVersion string

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
