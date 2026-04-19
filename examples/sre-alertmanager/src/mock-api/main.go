package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	failureRate := 0.3
	if v := os.Getenv("FAILURE_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			failureRate = f
		}
	}
	port := envOr("PORT", "8080")

	reg := prometheus.DefaultRegisterer
	h := newAPIHandler(failureRate, reg)

	mux := http.NewServeMux()
	mux.Handle("/api/status", h)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", healthzHandler)

	fmt.Printf(`{"msg":"mock-api starting","port":%q,"failure_rate":%f}`+"\n", port, failureRate)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		fmt.Fprintf(os.Stderr, `{"msg":"server error","error":%q}`+"\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
