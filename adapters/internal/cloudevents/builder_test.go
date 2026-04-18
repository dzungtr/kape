package cloudevents_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cebuilder "github.com/kape-io/kape/adapters/internal/cloudevents"
)

func TestBuild_ValidAlert(t *testing.T) {
	startsAt := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
	in := cebuilder.AlertInput{
		Subject:   "kape.events.security.cilium",
		Job:       "cilium",
		Alertname: "CiliumNetworkPolicyDrop",
		Labels: map[string]string{
			"alertname":    "CiliumNetworkPolicyDrop",
			"severity":     "warning",
			"kape_subject": "kape.events.security.cilium",
			"job":          "cilium",
		},
		Annotations:  map[string]string{"summary": "High rate of Cilium network policy drops"},
		StartsAt:     startsAt,
		GeneratorURL: "http://prometheus/graph",
	}

	event, err := cebuilder.Build(in)
	require.NoError(t, err)

	assert.Equal(t, "1.0", event.SpecVersion())
	assert.Equal(t, "kape.events.security.cilium", event.Type())
	assert.Equal(t, "alertmanager/cilium", event.Source())
	assert.Equal(t, "application/json", event.DataContentType())
	assert.Equal(t, startsAt, event.Time())
	assert.NotEmpty(t, event.ID())

	var data map[string]any
	require.NoError(t, event.DataAs(&data))
	assert.Equal(t, "CiliumNetworkPolicyDrop", data["alertname"])
	assert.Equal(t, "http://prometheus/graph", data["generatorURL"])
	labels, ok := data["labels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "kape.events.security.cilium", labels["kape_subject"])
}

func TestBuild_MissingSubject(t *testing.T) {
	in := cebuilder.AlertInput{
		Alertname: "SomeAlert",
		StartsAt:  time.Now(),
	}
	_, err := cebuilder.Build(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing kape_subject")
}

func TestBuild_EmptySubject(t *testing.T) {
	in := cebuilder.AlertInput{
		Subject:   "",
		Alertname: "SomeAlert",
		StartsAt:  time.Now(),
	}
	_, err := cebuilder.Build(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing kape_subject")
}

func TestBuild_MissingJob(t *testing.T) {
	in := cebuilder.AlertInput{
		Subject:  "kape.events.security.cilium",
		StartsAt: time.Now(),
	}
	event, err := cebuilder.Build(in)
	require.NoError(t, err)
	assert.Equal(t, "alertmanager/", event.Source())
}
