package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	natsgo "github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/kape-io/kape/adapters/internal/alertmanager"
	natspkg "github.com/kape-io/kape/adapters/internal/nats"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	playground := flag.Bool("playground", false, "Publish one sample alertmanager payload to NATS and exit")
	flag.Parse()

	natsURL := envOr("NATS_URL", natsgo.DefaultURL)
	port := envOr("PORT", "8080")
	publishTTL := envDuration("PUBLISH_TIMEOUT_SECONDS", 60)

	nc, err := natsgo.Connect(natsURL,
		natsgo.Name("kape-alertmanager-adapter"),
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(2*time.Second),
	)
	if err != nil {
		log.Fatal().Err(err).Str("nats_url", natsURL).Msg("failed to connect to NATS")
	}
	defer nc.Drain()
	log.Info().Str("nats_url", natsURL).Msg("connected to NATS")

	publisher, err := natspkg.NewPublisher(nc)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialise NATS publisher")
	}
	log.Info().Msg("KAPE_EVENTS stream provisioned")

	if *playground {
		runPlayground(publisher, publishTTL)
		return
	}

	handler := alertmanager.NewHandler(publisher, log.Logger, publishTTL)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Post("/webhook", handler.ServeHTTP)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if nc.IsConnected() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	r.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf(":%s", port)
	log.Info().Str("addr", addr).Msg("kape-alertmanager-adapter starting")
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal().Err(err).Msg("server exited")
	}
}

func runPlayground(publisher *natspkg.Publisher, ttl time.Duration) {
	payload := alertmanager.WebhookPayload{
		Receiver: "kape-webhook",
		Status:   "firing",
		Alerts: []alertmanager.Alert{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "MockApiHighErrorRate",
					"namespace": "kape-examples",
					"severity":  "critical",
				},
				Annotations: map[string]string{
					"summary":     "Mock API error rate above 10%",
					"description": "Error rate is 42% over the last 5 minutes.",
				},
				StartsAt:     time.Now().UTC(),
				EndsAt:       time.Time{},
				GeneratorURL: "http://playground/alerts",
				Fingerprint:  "playground-001",
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Fatal().Err(err).Msg("marshal playground payload")
	}

	h := alertmanager.NewHandler(publisher, log.Logger, ttl)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		log.Fatal().Int("status", rr.Code).Str("body", rr.Body.String()).Msg("playground publish failed")
	}
	log.Info().Msg("playground: published alertmanager sample event to NATS")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, defaultSeconds int) time.Duration {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return time.Duration(defaultSeconds) * time.Second
}
