package main

// Tests for the SLM client (issue #231): fake transport asserts request
// shape (model ids, temperature 0, max_tokens caps, no reasoning, configured
// base URL) and response parsing; failure injection covers timeout, 429,
// 5xx, and unparseable bodies — no retries, no panics.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func monitorConfig() MonitorConfig {
	m := DefaultConfig().Monitor
	m.APIKey = "test-key"
	return m
}

// fakeTransport captures the last request and returns the canned response.
type fakeTransport struct {
	status  int
	body    string
	sleep   time.Duration // >0 simulates a slow upstream (timeout path)
	request *http.Request
	reqBody string
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.request = r
	if b, err := readAll(r); err == nil {
		f.reqBody = b
	}
	if f.sleep > 0 {
		time.Sleep(f.sleep)
	}
	return &http.Response{
		StatusCode: f.status,
		Body:       newReadCloser(f.body),
		Header:     http.Header{},
		Request:    r,
	}, nil
}

func readAll(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}
	b, err := io.ReadAll(r.Body)
	return string(b), err
}

func newReadCloser(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }

// monitorLiveKey returns the live OpenRouter key when one is in the
// environment (the empirical smoke path for #231).
func monitorLiveKey() string {
	if k := os.Getenv("KAPE_MONITOR_OPENROUTER_API_KEY"); k != "" {
		return k
	}
	return os.Getenv("OPENROUTER_API_KEY")
}

func newTestClient(m MonitorConfig, t *fakeTransport) *SLMClient {
	return NewSLMClient(m, &http.Client{Transport: t, Timeout: 2 * time.Second})
}

func TestSummarizeRequestShape(t *testing.T) {
	m := monitorConfig()
	m.BaseURL = "http://gateway.example/v1"
	ft := &fakeTransport{status: 200, body: `{"choices":[{"message":{"content":"the summary"}}]}`}
	c := newTestClient(m, ft)

	got, err := c.Summarize(context.Background(), "transcript text")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if got != "the summary" {
		t.Fatalf("got %q, want the summary", got)
	}
	if ft.request.URL.String() != "http://gateway.example/v1/chat/completions" {
		t.Fatalf("url = %s, want configured base URL + /chat/completions", ft.request.URL)
	}
	if got := ft.request.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q", got)
	}
	assertChatBody(t, ft.reqBody, m.SummarizerModel, 400)
}

func TestSummarizeTerminalIncludesDeliverableClause(t *testing.T) {
	ft := &fakeTransport{status: 200, body: `{"choices":[{"message":{"content":"done"}}]}`}
	c := newTestClient(monitorConfig(), ft)
	if _, err := c.SummarizeTerminal(context.Background(), "t"); err != nil {
		t.Fatalf("SummarizeTerminal: %v", err)
	}
	if !strings.Contains(ft.reqBody, "deliverable references") {
		t.Fatalf("terminal prompt missing deliverable clause: %s", ft.reqBody)
	}
}

func TestClassifyRequestShapeAgainstConfiguredBaseURL(t *testing.T) {
	m := monitorConfig()
	m.BaseURL = "http://local-gw:8080/api/v1"
	ft := &fakeTransport{status: 200, body: `{"answers":{"verdict":{"choice":"healthy"},"confidence":{"score":1.5}}}`}
	c := newTestClient(m, ft)

	tel := TurnTelemetry{
		Summary: "agent fixed tests", ElapsedSeconds: 180,
		SecondsSinceLastRecord: 3, LastRecordType: "tool_call",
		RecordCount: 42, TurnPrompt: "fix failing unit tests in controller module",
	}
	got, err := c.Classify(context.Background(), tel)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got.Verdict != VerdictHealthy || got.Confidence != "likely" {
		t.Fatalf("verdict = %+v", got)
	}
	// Decisions endpoint derived from the configured base URL — sibling of
	// the /v1 tree, never hardcoded.
	if ft.request.URL.String() != "http://local-gw:8080/api/alpha/decisions" {
		t.Fatalf("url = %s, want base-URL-derived /alpha/decisions", ft.request.URL)
	}
	if got := ft.request.URL.Host; got != "local-gw:8080" {
		t.Fatalf("host = %s, want the configured host", got)
	}
	if ft.reqBody == "" || !strings.Contains(ft.reqBody, `"model":"scaledown/classify-1"`) {
		t.Fatalf("classifier model missing from request: %s", ft.reqBody)
	}
	// Turn telemetry rides in state text (live-pinned: state must be a string).
	if !strings.Contains(ft.reqBody, "seconds_since_last_record: 3") ||
		!strings.Contains(ft.reqBody, "last_record_type: tool_call") ||
		!strings.Contains(ft.reqBody, "agent fixed tests") {
		t.Fatalf("state text missing telemetry: %s", ft.reqBody)
	}
	// The six verdict criteria are the verdict semantics — normative.
	for _, v := range []string{"healthy", "looping", "blocked", "degraded", "finished", "failed"} {
		if !strings.Contains(ft.reqBody, `"`+v+`"`) {
			t.Fatalf("verdict criterion %q missing from questions: %s", v, ft.reqBody)
		}
	}
}

func TestSummarizeNoReasoningTemperatureZero(t *testing.T) {
	ft := &fakeTransport{status: 200, body: `{"choices":[{"message":{"content":"s"}}]}`}
	c := newTestClient(monitorConfig(), ft)
	if _, err := c.Summarize(context.Background(), "t"); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	assertChatBody(t, ft.reqBody, "~deepseek/deepseek-v4-flash-latest", 400)
}

// assertChatBody asserts the chat-completions request contract: correct
// model, temperature 0, max_tokens cap, and no reasoning parameters.
func assertChatBody(t *testing.T, body, model string, wantMaxTokens int) {
	t.Helper()
	for _, want := range []string{
		fmt.Sprintf(`"model":"%s"`, model),
		`"temperature":0`,
		fmt.Sprintf(`"max_tokens":%d`, wantMaxTokens),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("request body missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, "reasoning") {
		t.Fatalf("request body carries reasoning parameters: %s", body)
	}
}

func TestClassifyParsesAllVerdicts(t *testing.T) {
	for _, v := range []string{VerdictHealthy, VerdictLooping, VerdictBlocked, VerdictDegraded, VerdictFinished, VerdictFailed} {
		ft := &fakeTransport{status: 200, body: fmt.Sprintf(
			`{"answers":{"verdict":{"choice":"%s"},"confidence":{"score":2.0}}}`, v)}
		c := newTestClient(monitorConfig(), ft)
		got, err := c.Classify(context.Background(), TurnTelemetry{})
		if err != nil {
			t.Fatalf("Classify(%s): %v", v, err)
		}
		if got.Verdict != v {
			t.Fatalf("verdict = %q, want %q", got.Verdict, v)
		}
		if got.Confidence != "certain" {
			t.Fatalf("confidence = %q, want certain", got.Confidence)
		}
	}
}

func TestClassifyConfidenceLevels(t *testing.T) {
	cases := map[float64]string{0.1: "uncertain", 0.5: "uncertain", 1.0: "likely", 1.9: "certain", 2.0: "certain"}
	for score, want := range cases {
		got := confidenceLevel(map[string]any{"score": score})
		if got != want {
			t.Fatalf("confidenceLevel(%v) = %q, want %q", score, got, want)
		}
	}
}

func TestClassifyUnknownVerdictIsError(t *testing.T) {
	ft := &fakeTransport{status: 200, body: `{"answers":{"verdict":{"choice":"stuck"},"confidence":{"score":1}}}`}
	c := newTestClient(monitorConfig(), ft)
	if _, err := c.Classify(context.Background(), TurnTelemetry{}); err == nil {
		t.Fatalf("unknown verdict accepted, want error")
	}
}

func TestClassifyUnparseableBodyIsError(t *testing.T) {
	ft := &fakeTransport{status: 200, body: "not json"}
	c := newTestClient(monitorConfig(), ft)
	if _, err := c.Classify(context.Background(), TurnTelemetry{}); err == nil {
		t.Fatalf("garbage body accepted, want error")
	}
}

func TestSLMFailurePathsNoRetry(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"429", http.StatusTooManyRequests, `{"error":{"message":"rate limited"}}`},
		{"5xx", http.StatusInternalServerError, `{"error":{"message":"boom"}}`},
	}
	for _, tc := range cases {
		calls := 0
		c := NewSLMClient(monitorConfig(), &http.Client{Transport: countingTransport{status: tc.status, body: tc.body, calls: &calls}})
		if _, err := c.Summarize(context.Background(), "t"); err == nil {
			t.Fatalf("%s: Summarize accepted, want error", tc.name)
		}
		if _, err := c.Classify(context.Background(), TurnTelemetry{}); err == nil {
			t.Fatalf("%s: Classify accepted, want error", tc.name)
		}
		if calls != 2 {
			t.Fatalf("%s: %d calls, want exactly 2 (one per call, no retry)", tc.name, calls)
		}
	}
}

type countingTransport struct {
	status int
	body   string
	calls  *int
}

func (ct countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	*ct.calls++
	return &http.Response{StatusCode: ct.status, Body: newReadCloser(ct.body), Header: http.Header{}}, nil
}

func TestSLMTimeout(t *testing.T) {
	ft := &fakeTransport{status: 200, sleep: 2 * time.Second, body: `{}`}
	c := newTestClient(monitorConfig(), ft)
	c.httpClient.Timeout = 50 * time.Millisecond
	if _, err := c.Summarize(context.Background(), "t"); err == nil {
		t.Fatalf("Summarize accepted despite timeout, want error")
	}
}

func TestClassifyTurnPromptTruncated(t *testing.T) {
	long := strings.Repeat("x", 400)
	ft := &fakeTransport{status: 200, body: `{"answers":{"verdict":{"choice":"healthy"},"confidence":{"score":1}}}`}
	c := newTestClient(monitorConfig(), ft)
	if _, err := c.Classify(context.Background(), TurnTelemetry{TurnPrompt: long}); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	// ~200 chars cap: the 400-char prompt run must not ride in the request.
	if strings.Count(ft.reqBody, strings.Repeat("x", 201)) != 0 {
		t.Fatalf("turn prompt not truncated to ~200 chars")
	}
}

// liveSmoke hits the real OpenRouter endpoint when a key is available;
// skipped otherwise (final empirical verification also lands via #236).
func TestLiveSmoke(t *testing.T) {
	key := monitorLiveKey()
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	m := DefaultConfig().Monitor
	m.APIKey = key
	c := NewSLMClient(m, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	summary, err := c.Summarize(ctx, "[assistant] I'll fix the failing tests.\n[tool_result] 2 failures in TestX\n[assistant] Fixed TestX, re-running.")
	if err != nil {
		t.Fatalf("live Summarize: %v", err)
	}
	if summary == "" {
		t.Fatalf("live Summarize returned empty summary")
	}

	v, err := c.Classify(ctx, TurnTelemetry{
		Summary: summary, ElapsedSeconds: 180, SecondsSinceLastRecord: 3,
		LastRecordType: "tool_call", RecordCount: 42, TurnPrompt: "fix failing unit tests in controller module",
	})
	if err != nil {
		t.Fatalf("live Classify: %v", err)
	}
	if !validVerdicts[v.Verdict] {
		t.Fatalf("live verdict %q not in enum", v.Verdict)
	}
}

// httptest server round-trip sanity: the client works against a real
// HTTP server, not only the fake transport.
func TestSummarizeAgainstRealServer(t *testing.T) {
	if dialLocalBlocked() {
		t.Skip("loopback egress blocked by sandbox")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()
	m := monitorConfig()
	m.BaseURL = srv.URL + "/api/v1"
	c := NewSLMClient(m, nil)
	got, err := c.Summarize(context.Background(), "t")
	if err != nil || got != "ok" {
		t.Fatalf("Summarize = %q, %v", got, err)
	}
}

// dialLocalBlocked reports whether the environment forbids loopback egress
// (some sandboxes deny connecting to even their own httptest servers).
func dialLocalBlocked() bool {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	hc := srv.Client()
	resp, err := hc.Get(srv.URL)
	if err != nil {
		return true
	}
	resp.Body.Close()
	return false
}
