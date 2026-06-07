package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kape-io/kape/adapters/internal/audit"
)

type fakePublisher struct {
	published []ce.Event
	subjects  []string
}

func (f *fakePublisher) Publish(_ context.Context, subject string, event ce.Event) error {
	f.subjects = append(f.subjects, subject)
	f.published = append(f.published, event)
	return nil
}

func getCounterValue(t *testing.T, name string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() == name {
			var total float64
			for _, m := range mf.GetMetric() {
				total += m.GetCounter().GetValue()
			}
			return total
		}
	}
	return 0
}

type failingPublisher struct{}

func (f *failingPublisher) Publish(_ context.Context, _ string, _ ce.Event) error {
	return fmt.Errorf("nats unavailable")
}

func makeEventList(events []audit.Event) []byte {
	body, _ := json.Marshal(audit.EventList{
		APIVersion: "audit.k8s.io/v1",
		Kind:       "EventList",
		Items:      events,
	})
	return body
}

func sampleAuditEvent(auditID string) audit.Event {
	now := time.Now().UTC()
	return audit.Event{
		AuditID: auditID,
		Stage:   "ResponseComplete",
		Verb:    "get",
		User:    audit.UserInfo{Username: "admin"},
		ObjectRef: &audit.ObjectRef{
			Resource:  "secrets",
			Namespace: "prod",
			Name:      "db-creds",
		},
		ResponseStatus:           &audit.StatusInfo{Code: 200},
		RequestReceivedTimestamp: now,
		StageTimestamp:           now,
	}
}

func TestHandler_ValidEventList(t *testing.T) {
	pub := &fakePublisher{}
	logger := zerolog.Nop()
	h := audit.NewHandler(pub, logger, 60*time.Second, "test-cluster")

	events := []audit.Event{
		sampleAuditEvent("audit-id-1"),
		sampleAuditEvent("audit-id-2"),
	}

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(makeEventList(events)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	require.Len(t, pub.published, 2)
	assert.Equal(t, "audit-id-1", pub.published[0].ID())
	assert.Equal(t, "audit-id-2", pub.published[1].ID())
	assert.Equal(t, "kape.events.security.audit", pub.published[0].Type())
	assert.Equal(t, "kape.events.security.audit", pub.subjects[0])
}

func TestHandler_InvalidJSON(t *testing.T) {
	pub := &fakePublisher{}
	logger := zerolog.Nop()
	h := audit.NewHandler(pub, logger, 60*time.Second, "test-cluster")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Empty(t, pub.published)
}

func TestHandler_PublishError(t *testing.T) {
	pub := &failingPublisher{}
	logger := zerolog.Nop()
	h := audit.NewHandler(pub, logger, 60*time.Second, "test-cluster")

	events := []audit.Event{sampleAuditEvent("audit-id-fail")}

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(makeEventList(events)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	errsBefore := getCounterValue(t, "kape_audit_publish_errors_total")
	h.ServeHTTP(rr, req)

	// Publish failure is logged but must not fail the HTTP response.
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, errsBefore+1, getCounterValue(t, "kape_audit_publish_errors_total"))
}

func TestHandler_EmptyItems(t *testing.T) {
	pub := &fakePublisher{}
	logger := zerolog.Nop()
	h := audit.NewHandler(pub, logger, 60*time.Second, "test-cluster")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(makeEventList(nil)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Empty(t, pub.published)
}
