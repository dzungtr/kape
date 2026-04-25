package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// scenario describes a failure scenario with realistic log context.
type scenario struct {
	reason    string
	message   string
	latencyMs int64 // simulated latency for this failure type
}

// scenarios maps each error reason to a realistic description and latency.
// The message field becomes the "message" in the structured log, giving the
// KapeHandler agent concrete evidence to reason over.
var scenarios = []scenario{
	{
		reason:    "db_timeout",
		message:   "Database connection pool exhausted after 3 retries. Query timed out waiting for available connection.",
		latencyMs: 5000,
	},
	{
		reason:    "upstream_unavailable",
		message:   "Upstream payment service returned HTTP 503. Circuit breaker is OPEN. Last successful call was 42s ago.",
		latencyMs: 1200,
	},
	{
		reason:    "nil_pointer",
		message:   "Unhandled nil pointer dereference in order processing pipeline at step validate_inventory. Order ID was not pre-loaded.",
		latencyMs: 3,
	},
}

type requestLog struct {
	RequestID   string `json:"request_id"`
	StatusCode  int    `json:"status_code"`
	LatencyMs   int64  `json:"latency_ms"`
	ErrorReason string `json:"error_reason,omitempty"`
	Message     string `json:"message"`
	Timestamp   string `json:"timestamp"`
}

type apiHandler struct {
	failureRate   float64
	requestsTotal *prometheus.CounterVec
	latencyHist   prometheus.Histogram
}

func newAPIHandler(failureRate float64, reg prometheus.Registerer) *apiHandler {
	factory := promauto.With(reg)
	return &apiHandler{
		failureRate: failureRate,
		requestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "mock_api_requests_total",
			Help: "Total requests partitioned by status.",
		}, []string{"status"}),
		latencyHist: factory.NewHistogram(prometheus.HistogramOpts{
			Name:    "mock_api_latency_seconds",
			Help:    "Request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}),
	}
}

func (h *apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := fmt.Sprintf("%d", start.UnixNano())

	entry := requestLog{
		RequestID: reqID,
		Timestamp: start.UTC().Format(time.RFC3339),
	}

	if rand.Float64() < h.failureRate {
		sc := scenarios[rand.Intn(len(scenarios))]
		entry.StatusCode = http.StatusInternalServerError
		entry.ErrorReason = sc.reason
		entry.Message = sc.message
		entry.LatencyMs = sc.latencyMs

		h.requestsTotal.WithLabelValues("error").Inc()
		h.latencyHist.Observe(float64(sc.latencyMs) / 1000.0)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": sc.reason})
	} else {
		entry.StatusCode = http.StatusOK
		entry.LatencyMs = time.Since(start).Milliseconds()
		entry.Message = "Request processed successfully."

		h.requestsTotal.WithLabelValues("success").Inc()
		h.latencyHist.Observe(float64(entry.LatencyMs) / 1000.0)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}

	logLine, _ := json.Marshal(entry)
	fmt.Println(string(logLine))
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
