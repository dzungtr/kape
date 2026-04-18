package alertmanager

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	cebuilder "github.com/kape-io/kape/adapters/internal/cloudevents"
)

// Publisher is the interface the Handler uses to emit CloudEvents.
type Publisher interface {
	Publish(ctx context.Context, subject string, event ce.Event) error
}

// Handler processes AlertManager webhook payloads.
type Handler struct {
	publisher       Publisher
	logger          zerolog.Logger
	publishTTL      time.Duration
	eventsReceived  prometheus.Counter
	eventsPublished prometheus.Counter
	publishErrors   prometheus.Counter
}

// NewHandler creates a Handler and registers Prometheus metrics.
func NewHandler(pub Publisher, logger zerolog.Logger, publishTTL time.Duration) *Handler {
	reg := prometheus.DefaultRegisterer
	eventsReceived := mustOrExisting(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kape_alertmanager_events_received_total",
		Help: "Total AlertManager alerts received.",
	}))
	eventsPublished := mustOrExisting(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kape_alertmanager_events_published_total",
		Help: "Total CloudEvents successfully published to NATS.",
	}))
	publishErrors := mustOrExisting(reg, prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kape_alertmanager_publish_errors_total",
		Help: "Total NATS publish failures after retry TTL.",
	}))

	return &Handler{
		publisher:       pub,
		logger:          logger,
		publishTTL:      publishTTL,
		eventsReceived:  eventsReceived,
		eventsPublished: eventsPublished,
		publishErrors:   publishErrors,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var payload WebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.logger.Warn().Err(err).Msg("failed to decode webhook payload")
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	for _, alert := range payload.Alerts {
		h.eventsReceived.Inc()

		event, err := cebuilder.Build(cebuilder.AlertInput{
			Subject:      alert.Labels["kape_subject"],
			Job:          alert.Labels["job"],
			Alertname:    alert.Labels["alertname"],
			Labels:       alert.Labels,
			Annotations:  alert.Annotations,
			StartsAt:     alert.StartsAt,
			GeneratorURL: alert.GeneratorURL,
		})
		if err != nil {
			h.logger.Warn().Err(err).
				Str("alertname", alert.Labels["alertname"]).
				Msg("skipping alert: missing kape_subject")
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), h.publishTTL)
		pubErr := h.publisher.Publish(ctx, event.Type(), event)
		cancel()

		if pubErr != nil {
			h.publishErrors.Inc()
			h.logger.Error().Err(pubErr).
				Str("subject", event.Type()).
				Str("event_id", event.ID()).
				RawJSON("event", mustMarshal(event)).
				Msg("dropped alert: publish failed after retry TTL")
			continue
		}

		h.eventsPublished.Inc()
		h.logger.Info().
			Str("subject", event.Type()).
			Str("event_id", event.ID()).
			Str("alertname", alert.Labels["alertname"]).
			Msg("published cloud event")
	}

	w.WriteHeader(http.StatusOK)
}

// mustOrExisting registers c and returns it; if already registered, returns the existing collector.
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
