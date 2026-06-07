package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

// Publisher is the interface the Handler uses to emit CloudEvents.
type Publisher interface {
	Publish(ctx context.Context, subject string, event ce.Event) error
}

// Handler processes K8s audit webhook payloads.
type Handler struct {
	publisher       Publisher
	logger          zerolog.Logger
	publishTTL      time.Duration
	clusterName     string
	eventsReceived  prometheus.Counter
	eventsPublished prometheus.Counter
	publishErrors   prometheus.Counter
}

// NewHandler creates a Handler and registers Prometheus metrics.
func NewHandler(pub Publisher, logger zerolog.Logger, publishTTL time.Duration, clusterName string) *Handler {
	reg := prometheus.DefaultRegisterer
	eventsReceived := mustOrExisting(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kape_audit_events_received_total",
		Help: "Total individual audit events received from the API server.",
	}))
	eventsPublished := mustOrExisting(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kape_audit_events_published_total",
		Help: "Total CloudEvents successfully published to NATS.",
	}))
	publishErrors := mustOrExisting(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kape_audit_publish_errors_total",
		Help: "Total NATS publish failures after retry TTL.",
	}))

	return &Handler{
		publisher:       pub,
		logger:          logger,
		publishTTL:      publishTTL,
		clusterName:     clusterName,
		eventsReceived:  eventsReceived,
		eventsPublished: eventsPublished,
		publishErrors:   publishErrors,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var list EventList
	if err := json.NewDecoder(r.Body).Decode(&list); err != nil {
		h.logger.Warn().Err(err).Msg("failed to decode audit EventList")
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	for _, ev := range list.Items {
		h.eventsReceived.Inc()

		cloudEvent, err := BuildAudit(ev, h.clusterName)
		if err != nil {
			h.logger.Error().Err(err).Str("audit_id", ev.AuditID).Msg("failed to build CloudEvent")
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), h.publishTTL)
		pubErr := h.publisher.Publish(ctx, subject, cloudEvent)
		cancel()

		if pubErr != nil {
			h.publishErrors.Inc()
			h.logger.Error().Err(pubErr).
				Str("audit_id", ev.AuditID).
				Str("verb", ev.Verb).
				Str("resource", resourceOf(ev)).
				RawJSON("event", mustMarshal(cloudEvent)).
				Msg("dropped audit event: publish failed after retry TTL")
			continue
		}

		h.eventsPublished.Inc()
		h.logger.Info().
			Str("audit_id", ev.AuditID).
			Str("verb", ev.Verb).
			Str("resource", resourceOf(ev)).
			Msg("published audit CloudEvent")
	}

	w.WriteHeader(http.StatusOK)
}

func resourceOf(ev Event) string {
	if ev.ObjectRef != nil {
		return ev.ObjectRef.Resource
	}
	return ""
}

func mustOrExisting(reg prometheus.Registerer, c prometheus.Counter) prometheus.Counter {
	if err := reg.Register(c); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(prometheus.Counter)
		}
		panic(err)
	}
	return c
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
