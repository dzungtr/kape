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
	"github.com/google/uuid"
	natsgo "github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/kape-io/kape/adapters/internal/audit"
	natspkg "github.com/kape-io/kape/adapters/internal/nats"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	playground := flag.Bool("playground", false, "Publish one sample audit event to NATS and exit")
	flag.Parse()

	natsURL     := envOr("NATS_URL", natsgo.DefaultURL)
	port        := envOr("PORT", "8443")
	publishTTL  := envDuration("PUBLISH_TIMEOUT_SECONDS", 60)
	clusterName := envOr("CLUSTER_NAME", "unknown")
	tlsCertFile := envOr("TLS_CERT_FILE", "/etc/kape/tls/tls.crt")
	tlsKeyFile  := envOr("TLS_KEY_FILE", "/etc/kape/tls/tls.key")

	nc, err := natsgo.Connect(natsURL,
		natsgo.Name("kape-audit-adapter"),
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
		runPlayground(publisher, publishTTL, clusterName)
		return
	}

	handler := audit.NewHandler(publisher, log.Logger, publishTTL, clusterName)

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
	log.Info().Str("addr", addr).Str("cluster_name", clusterName).Msg("kape-audit-adapter starting")

	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}
	if err := srv.ListenAndServeTLS(tlsCertFile, tlsKeyFile); err != nil {
		log.Fatal().Err(err).Msg("server exited")
	}
}

func runPlayground(publisher *natspkg.Publisher, ttl time.Duration, clusterName string) {
	now := time.Now().UTC()
	ev := audit.Event{
		AuditID: "playground-" + uuid.New().String(),
		Stage:   "ResponseComplete",
		Verb:    "get",
		User: audit.UserInfo{
			Username: "system:serviceaccount:prod:api",
			Groups:   []string{"system:serviceaccounts"},
		},
		UserAgent: "kubectl/v1.35.0 (linux/amd64)",
		ObjectRef: &audit.ObjectRef{
			Resource:  "secrets",
			Namespace: "prod",
			Name:      "db-credentials",
		},
		ResponseStatus:           &audit.StatusInfo{Code: 200},
		RequestReceivedTimestamp: now,
		StageTimestamp:           now,
	}

	list := audit.EventList{
		APIVersion: "audit.k8s.io/v1",
		Kind:       "EventList",
		Items:      []audit.Event{ev},
	}

	body, err := json.Marshal(list)
	if err != nil {
		log.Fatal().Err(err).Msg("marshal playground payload")
	}

	h := audit.NewHandler(publisher, log.Logger, ttl, clusterName)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		log.Fatal().Int("status", rr.Code).Str("body", rr.Body.String()).Msg("playground publish failed")
	}
	log.Info().Str("audit_id", ev.AuditID).Msg("playground: published audit event to NATS")
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
