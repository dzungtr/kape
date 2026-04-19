package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReceiver_PostKapeDecision(t *testing.T) {
	rec := httptest.NewRecorder()
	body := `{
		"severity":"high",
		"root_cause":"db_timeout",
		"affected_service":"mock-api",
		"recommendation":"Check database connection pool settings.",
		"evidence_summary":"23 of 50 log lines showed db_timeout in the past minute."
	}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	var buf bytes.Buffer
	h := newReceiver(&buf)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 got %d", rec.Code)
	}
	out := buf.String()
	for _, want := range []string{"severity", "high", "root_cause", "db_timeout", "mock-api", "recommendation", "evidence_summary"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected log to contain %q, got:\n%s", want, out)
		}
	}
}

func TestReceiver_NonPostReturns405(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)

	var buf bytes.Buffer
	h := newReceiver(&buf)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 got %d", rec.Code)
	}
}

func TestReceiver_InvalidJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("not-json"))

	var buf bytes.Buffer
	h := newReceiver(&buf)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 even for invalid JSON, got %d", rec.Code)
	}
	if !strings.Contains(buf.String(), "not-json") {
		t.Errorf("expected raw body in log, got: %s", buf.String())
	}
}
