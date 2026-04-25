package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestAPIHandler_AlwaysSuccess(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := newAPIHandler(0.0, reg) // 0% failure rate
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 got %d", rec.Code)
		}
	}
}

func TestAPIHandler_AlwaysFail(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := newAPIHandler(1.0, reg) // 100% failure rate
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("expected 500 got %d", rec.Code)
		}
	}
}

func TestAPIHandler_FailLogHasMessage(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := newAPIHandler(1.0, reg)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 got %d", rec.Code)
	}
	// All scenarios must have non-empty message and latencyMs > 0
	for _, sc := range scenarios {
		if sc.message == "" {
			t.Errorf("scenario %q has empty message", sc.reason)
		}
		if sc.latencyMs <= 0 {
			t.Errorf("scenario %q has non-positive latencyMs", sc.reason)
		}
	}
}

func TestAPIHandler_PartialFailure(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := newAPIHandler(0.5, reg)
	successes, failures := 0, 0
	for i := 0; i < 1000; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			successes++
		} else {
			failures++
		}
	}
	// With 50% rate and 1000 samples, expect 350-650 failures
	if failures < 350 || failures > 650 {
		t.Errorf("expected ~500 failures, got %d out of 1000", failures)
	}
}

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	http.HandlerFunc(healthzHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 got %d", rec.Code)
	}
}
