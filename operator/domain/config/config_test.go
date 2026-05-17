package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
)

func TestKapeproxyImageRef_DefaultIsLatest(t *testing.T) {
	// D17: in-code default for kapeproxy is :latest, not a release pin.
	// Release pins live in helm/values.yaml, not in Go code.
	c := domainconfig.KapeConfig{} // nothing set
	assert.Equal(t, "kape/kapeproxy:latest", c.KapeproxyImageRef(),
		"D17: in-code default for kapeproxy is :latest, not a release pin")
}

func TestKapeproxyImageRef_WithDefaults_IsLatest(t *testing.T) {
	// D17: WithDefaults must set :latest, not :stub or a release pin.
	c := domainconfig.KapeConfig{}.WithDefaults()
	assert.Equal(t, "latest", c.KapeproxyImageVersion,
		"D17: WithDefaults must set :latest, not :stub or a release pin")
	assert.Equal(t, "kape/kapeproxy:latest", c.KapeproxyImageRef())
}
