package main

// SLM client (issue #231): wraps the two API calls of the assessment
// pipeline against any OpenRouter-compatible endpoint (issue #230, Q11 —
// the configured base URL drives every request, never a hardcoded host).
//
//   - Summarize / SummarizeTerminal: chat completions against the
//     summarizer model (temperature 0, no reasoning, max_tokens 400).
//   - Classify: OpenRouter Alpha decisions request against the
//     decision-modality classifier (temperature and token caps do not
//     apply — the decisions API returns structured answers, 2 output
//     tokens live).
//
// Live-pinned wire shape (empirical smoke run while implementing #231,
// recorded in dzungtr/kape#230 Handoffs):
//   - Decisions endpoint is POST <api-root>/alpha/decisions — a sibling of
//     the chat-completions /v1 tree, not under it. The client derives it
//     from the configured base URL by stripping a trailing "/v1".
//   - `state` must be a string (or {text,...} object): the turn telemetry
//     is serialized into state.text. Numeric state fields are rejected.
//   - Response: answers.verdict.choice (string), answers.confidence.score
//     (float, legend 0=uncertain / 1=likely / 2=certain). The response
//     carries NO free-text reason — Verdict.Reason stays empty.
//
// Per-call timeout, no retries (issue #230, Q7 — silent-skip semantics and
// the metric counter land in #232; this client just returns errors).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Verdict values (issue #230, Q17 — single responsibility: one owner + one
// action class per verdict).
const (
	VerdictHealthy  = "healthy"
	VerdictLooping  = "looping"
	VerdictBlocked  = "blocked"
	VerdictDegraded = "degraded"
	VerdictFinished = "finished"
	VerdictFailed   = "failed"
)

// validVerdicts is the closed verdict enum the classifier may return;
// anything else is an unparseable response.
var validVerdicts = map[string]bool{
	VerdictHealthy: true, VerdictLooping: true, VerdictBlocked: true,
	VerdictDegraded: true, VerdictFinished: true, VerdictFailed: true,
}

// TurnTelemetry is the classifier input alongside the summary (issue #230,
// Q9). Silence disambiguation rides on LastRecordType: awaiting completion
// → provider suspicion (degraded); awaiting tool result → likely healthy.
type TurnTelemetry struct {
	Summary                string  // the summarizer's output
	ElapsedSeconds         float64 // seconds since turn start
	SecondsSinceLastRecord float64 // silence signal
	LastRecordType         string  // assistant | tool_call | tool_result | error | notice
	RecordCount            int     // records in the transcript window
	TurnPrompt             string  // what the agent was asked to do, first ~200 chars
}

// Verdict is the internal assessment schema (issue #230, Q9).
type Verdict struct {
	Verdict    string // one of the six Verdict* constants
	Confidence string // uncertain | likely | certain
	Reason     string // "" for the decisions path — the response carries none
}

// Turn budget caps (issue #230, Q10).
const (
	summarizeMaxTokens = 400
	turnPromptMaxChars = 200
)

// SLMClient issues the assessment-pipeline calls against the configured
// OpenRouter-compatible endpoint. httpClient is injectable for fakes;
// the zero-derived default uses the per-call timeout.
type SLMClient struct {
	baseURL         string // e.g. https://openrouter.ai/api/v1
	decisionsURL    string // derived from baseURL (see NewSLMClient)
	apiKey          string
	summarizerModel string
	classifierModel string
	httpClient      *http.Client
}

// NewSLMClient builds a client from the monitor config. The decisions URL
// is derived from the configured base URL: OpenRouter serves the Alpha
// decisions API at <api-root>/alpha/decisions, a sibling of the /v1 chat
// tree (live smoke — /api/v1/decisions and /api/v1/alpha/decisions are
// both 404). Pointing Monitor.BaseURL at a compatible endpoint changes
// config only, never code.
func NewSLMClient(m MonitorConfig, httpClient *http.Client) *SLMClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: m.Timeout}
	}
	return &SLMClient{
		baseURL:         strings.TrimSuffix(m.BaseURL, "/"),
		decisionsURL:    strings.TrimSuffix(strings.TrimSuffix(m.BaseURL, "/"), "/v1") + "/alpha/decisions",
		apiKey:          m.APIKey,
		summarizerModel: m.SummarizerModel,
		classifierModel: m.ClassifierModel,
		httpClient:      httpClient,
	}
}

// Summarize runs the mid-turn summarizer prompt over the transcript window.
func (c *SLMClient) Summarize(ctx context.Context, transcript string) (string, error) {
	return c.summarize(ctx, transcript, false)
}

// SummarizeTerminal runs the terminal summarizer prompt: same family,
// clause 5 additionally demands deliverable references (PR numbers, URLs,
// branch names, issue numbers) preserved verbatim (issue #230, Q17).
func (c *SLMClient) SummarizeTerminal(ctx context.Context, transcript string) (string, error) {
	return c.summarize(ctx, transcript, true)
}

// summarizePrompt is the summarizer prompt family (issue #230, Q17):
// capture (1) the task, (2) what the agent has done so far — key actions,
// tool calls, files/PRs/issues touched, (3) current focus, (4) errors,
// retries, repetition patterns, (5) terminal-only: final outcomes with
// deliverable references preserved verbatim. Concrete and factual, max
// ~300 words (fits the 400-token output cap).
func summarizePrompt(transcript string, terminal bool) string {
	var b strings.Builder
	b.WriteString("Summarize this software-agent turn transcript. Capture:\n")
	b.WriteString("1. What the agent was asked to do.\n")
	b.WriteString("2. What it has done so far — key actions, tool calls, files/PRs/issues touched.\n")
	b.WriteString("3. Its current focus.\n")
	b.WriteString("4. Errors, retries, and repetition patterns.\n")
	if terminal {
		b.WriteString("5. Final outcomes, with deliverable references (PR numbers, URLs, branch names, issue numbers) preserved verbatim.\n")
	}
	b.WriteString("Be concrete and factual; no speculation beyond the transcript. Max ~300 words.\n\nTranscript:\n")
	b.WriteString(transcript)
	return b.String()
}

func (c *SLMClient) summarize(ctx context.Context, transcript string, terminal bool) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	callCtx, cancel := context.WithTimeout(ctx, c.httpClient.Timeout)
	defer cancel()

	body := map[string]any{
		"model":       c.summarizerModel,
		"temperature": 0,
		"max_tokens":  summarizeMaxTokens,
		"messages": []map[string]string{
			{"role": "user", "content": summarizePrompt(transcript, terminal)},
		},
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := c.post(callCtx, c.baseURL+"/chat/completions", body, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("slm summarize: empty response from %s", c.summarizerModel)
	}
	return out.Choices[0].Message.Content, nil
}

// decisionsRequestBody is the live-pinned Alpha decisions request: the
// question definitions ARE the verdict semantics (issue #230, Q17); state
// must be a string per the ScaleDown API.
type decisionsRequestBody struct {
	Model     string         `json:"model"`
	Questions map[string]any `json:"questions"`
	State     string         `json:"state"`
}

// verdictQuestions is the normative question set: verdict is a choice over
// the six verdicts (criteria definitions normative), confidence is a score
// with three legend levels.
func verdictQuestions() map[string]any {
	return map[string]any{
		"verdict": map[string]any{
			"type":         "choice",
			"instructions": "Classify the state of this software agent turn.",
			"criteria": map[string]string{
				VerdictHealthy:  "The agent is progressing toward its task, including slow-but-steady long-running work.",
				VerdictLooping:  "The agent is repeating the same actions or drifting off task — model misbehavior.",
				VerdictBlocked:  "The agent correctly stopped on a missing environment prerequisite — credentials, config, tools, permissions, resources, or denied egress.",
				VerdictDegraded: "The LLM provider or platform infrastructure is failing — provider errors, timeouts, or no responses arriving.",
				VerdictFinished: "The agent has completed its task.",
				VerdictFailed:   "The agent errored out, hit context limits, or settled without completing the task.",
			},
		},
		"confidence": map[string]any{
			"type":         "score",
			"instructions": "How confident is the verdict?",
			"criteria":     []string{"uncertain", "likely", "certain"},
		},
	}
}

// Classify runs the decision-modality classifier over summary + turn
// telemetry and parses the structured answer into a Verdict.
func (c *SLMClient) Classify(ctx context.Context, t TurnTelemetry) (Verdict, error) {
	if ctx.Err() != nil {
		return Verdict{}, ctx.Err()
	}
	callCtx, cancel := context.WithTimeout(ctx, c.httpClient.Timeout)
	defer cancel()

	// Live-pinned: ScaleDown state must be a string — serialize the
	// telemetry (spec Q9 field names preserved) into state.text.
	body := decisionsRequestBody{
		Model:     c.classifierModel,
		Questions: verdictQuestions(),
		State: fmt.Sprintf(
			"task: %s\nelapsed_seconds: %g\nseconds_since_last_record: %g\nlast_record_type: %s\nrecord_count: %d\nsummary: %s",
			truncate(t.TurnPrompt, turnPromptMaxChars), t.ElapsedSeconds, t.SecondsSinceLastRecord,
			t.LastRecordType, t.RecordCount, t.Summary,
		),
	}

	var out struct {
		Answers map[string]any `json:"answers"`
	}
	if err := c.post(callCtx, c.decisionsURL, body, &out); err != nil {
		return Verdict{}, err
	}

	v, err := parseDecisionAnswers(out.Answers)
	if err != nil {
		return Verdict{}, err
	}
	return v, nil
}

// parseDecisionAnswers extracts {verdict, confidence, reason} from the
// decisions response: answers.verdict.choice + answers.confidence.score
// (legend 0=uncertain / 1=likely / 2=certain; midpoint thresholds). The
// response carries no free-text reason — Reason stays "".
func parseDecisionAnswers(answers map[string]any) (Verdict, error) {
	if answers == nil {
		return Verdict{}, fmt.Errorf("slm classify: missing answers")
	}
	vObj, _ := answers["verdict"].(map[string]any)
	choice, _ := vObj["choice"].(string)
	if !validVerdicts[choice] {
		return Verdict{}, fmt.Errorf("slm classify: unknown verdict %q", choice)
	}
	conf := confidenceLevel(answers["confidence"])
	return Verdict{Verdict: choice, Confidence: conf}, nil
}

// confidenceLevel maps a score answer (float, legend 0/1/2) to its level
// string via midpoint thresholds.
func confidenceLevel(raw any) string {
	score, ok := raw.(map[string]any)
	if !ok {
		return "uncertain"
	}
	f, ok := score["score"].(float64)
	if !ok {
		return "uncertain"
	}
	switch {
	case f <= 0.5:
		return "uncertain"
	case f <= 1.5:
		return "likely"
	default:
		return "certain"
	}
}

func (c *SLMClient) post(ctx context.Context, url string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("slm: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("slm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slm: call %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("slm: call %s: HTTP %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("slm: unparseable response from %s: %w", url, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
