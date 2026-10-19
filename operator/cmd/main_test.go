package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestControllerWiringParsesChartEnv pins the chart → operator contract for the
// KapeAgentServer cluster wiring: the chart renders these as JSON env values
// (charts/kape/templates/operator/deployment.yaml), the operator decodes them
// into domainconfig.ControllerWiring verbatim.
func TestControllerWiringParsesChartEnv(t *testing.T) {
	cfg := &config{
		ControllerGatewayAddr:     "openshell.openshell.svc.cluster.local:8080",
		ControllerCredsDir:        "/var/run/kape-controller/creds",
		ControllerCredsSecret:     "kape-controller-creds",
		ControllerDefaultProvider: "openrouter",
		ControllerTransport:       "pull",
		ControllerEventStore:      "/var/lib/kape-controller/controller-events.db",
		ControllerExtraEnv:        `[{"name":"KAPE_MONITOR_INTERVAL_SECONDS","value":"60"},{"name":"KAPE_MONITOR_OPENROUTER_API_KEY","valueFrom":{"secretKeyRef":{"name":"monitor-key","key":"OPENROUTER_API_KEY"}}}]`,
		ControllerExtraEnvFrom:    `[{"configMapRef":{"name":"controller-extra"}}]`,
	}

	wiring, err := controllerWiring(cfg)
	require.NoError(t, err)
	assert.Equal(t, "openshell.openshell.svc.cluster.local:8080", wiring.GatewayAddr)
	assert.Equal(t, "kape-controller-creds", wiring.CredsSecret)
	assert.Equal(t, "pull", wiring.Transport)
	assert.Empty(t, wiring.Missing())
	require.Len(t, wiring.ExtraEnv, 2)
	assert.Equal(t, "60", wiring.ExtraEnv[0].Value)
	require.NotNil(t, wiring.ExtraEnv[1].ValueFrom)
	assert.Equal(t, "OPENROUTER_API_KEY", wiring.ExtraEnv[1].ValueFrom.SecretKeyRef.Key)
	require.Len(t, wiring.ExtraEnvFrom, 1)
	assert.Equal(t, "controller-extra", wiring.ExtraEnvFrom[0].ConfigMapRef.Name)
}

// TestControllerWiringRejectsMalformedJSON: a chart value typo fails operator
// startup loudly instead of silently dropping feature wiring.
func TestControllerWiringRejectsMalformedJSON(t *testing.T) {
	_, err := controllerWiring(&config{ControllerExtraEnv: "not-json"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller-extra-env")

	_, err = controllerWiring(&config{ControllerExtraEnvFrom: "{"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "controller-extra-env-from")
}
