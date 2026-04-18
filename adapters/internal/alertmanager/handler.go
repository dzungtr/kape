package alertmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
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

		event, err := buildEvent(alert)
		if err != nil {
			h.logger.Warn().Err(err).
				Str("alertname", alert.Labels["alertname"]).
				Msg("skipping alert: missing kape_subject")
			continue
		}

		ctx, cancel := context.WithTimeout(r.Context(), h.publishTTL)
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

// buildEvent constructs a CloudEvents 1.0 event from an Alert.
// Returns an error if the alert has no kape_subject label.
// This mirrors cebuilder.Build but lives here to avoid an import cycle
// (package alertmanager ← package cloudevents ← package alertmanager).
func buildEvent(alert Alert) (ce.Event, error) {
	subject := alert.Labels["kape_subject"]
	if subject == "" {
		return ce.Event{}, fmt.Errorf("missing kape_subject label on alert %q", alert.Labels["alertname"])
	}

	event := ce.NewEvent()
	event.SetSpecVersion("1.0")
	event.SetType(subject)
	event.SetSource(fmt.Sprintf("alertmanager/%s", alert.Labels["job"]))
	event.SetID(uuid.New().String())
	event.SetTime(alert.StartsAt)
	event.SetDataContentType("application/json")

	data := map[string]any{
		"alertname":    alert.Labels["alertname"],
		"labels":       alert.Labels,
		"annotations":  alert.Annotations,
		"startsAt":     alert.StartsAt,
		"generatorURL": alert.GeneratorURL,
	}
	if err := event.SetData("application/json", data); err != nil {
		return ce.Event{}, fmt.Errorf("setting event data: %w", err)
	}

	return event, nil
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
