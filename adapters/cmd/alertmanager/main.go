package main

import (
	"fmt"
	"net/http"
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

	publisher, err := natspkg.NewPublisher(nc, publishTTL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialise NATS publisher")
	}
	log.Info().Msg("KAPE_EVENTS stream provisioned")

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
