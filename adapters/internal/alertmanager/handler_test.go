package alertmanager_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kape-io/kape/adapters/internal/alertmanager"
)

type fakePublisher struct {
	published []ce.Event
}

func (f *fakePublisher) Publish(_ context.Context, _ string, event ce.Event) error {
	f.published = append(f.published, event)
	return nil
}

func makePayload(alerts []alertmanager.Alert) []byte {
	body, _ := json.Marshal(alertmanager.WebhookPayload{
		Receiver: "kape",
		Status:   "firing",
		Alerts:   alerts,
	})
	return body
}

func TestHandler_ValidAlerts(t *testing.T) {
	pub := &fakePublisher{}
	logger := zerolog.Nop()
	h := alertmanager.NewHandler(pub, logger, 60*time.Second)

	alerts := []alertmanager.Alert{
		{
			Labels: map[string]string{
				"alertname":    "CiliumDrop",
				"kape_subject": "kape.events.security.cilium",
				"job":          "cilium",
			},
			Annotations: map[string]string{"summary": "test"},
			StartsAt:    time.Now(),
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(makePayload(alerts)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, pub.published, 1)
	assert.Equal(t, "kape.events.security.cilium", pub.published[0].Type())
}

func TestHandler_AlertMissingKapeSubject(t *testing.T) {
	pub := &fakePublisher{}
	logger := zerolog.Nop()
	h := alertmanager.NewHandler(pub, logger, 60*time.Second)

	alerts := []alertmanager.Alert{
		{
			Labels:   map[string]string{"alertname": "NoSubjectAlert"},
			StartsAt: time.Now(),
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(makePayload(alerts)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, pub.published)
}

func TestHandler_MixedAlerts(t *testing.T) {
	pub := &fakePublisher{}
	logger := zerolog.Nop()
	h := alertmanager.NewHandler(pub, logger, 60*time.Second)

	alerts := []alertmanager.Alert{
		{
			Labels:   map[string]string{"alertname": "NoSubject"},
			StartsAt: time.Now(),
		},
		{
			Labels:   map[string]string{"alertname": "HasSubject", "kape_subject": "kape.events.security.cilium"},
			StartsAt: time.Now(),
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(makePayload(alerts)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, pub.published, 1)
	assert.Equal(t, "kape.events.security.cilium", pub.published[0].Type())
}

func TestHandler_InvalidJSON(t *testing.T) {
	pub := &fakePublisher{}
	logger := zerolog.Nop()
	h := alertmanager.NewHandler(pub, logger, 60*time.Second)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
