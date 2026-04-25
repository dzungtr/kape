package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type receiver struct {
	out io.Writer
}

func newReceiver(out io.Writer) *receiver {
	return &receiver{out: out}
}

// kapeDecision holds the expected fields from KapeHandler's structured output.
type kapeDecision struct {
	Severity        string `json:"severity"`
	RootCause       string `json:"root_cause"`
	AffectedService string `json:"affected_service"`
	Recommendation  string `json:"recommendation"`
	EvidenceSummary string `json:"evidence_summary"`
}

func (rec *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ts := time.Now().UTC().Format(time.RFC3339)

	var d kapeDecision
	if err := json.Unmarshal(body, &d); err == nil && d.Severity != "" {
		fmt.Fprintf(rec.out, "\n========================================\n")
		fmt.Fprintf(rec.out, "  KAPE SRE DECISION  [%s]\n", ts)
		fmt.Fprintf(rec.out, "========================================\n")
		fmt.Fprintf(rec.out, "  severity         : %s\n", d.Severity)
		fmt.Fprintf(rec.out, "  affected_service : %s\n", d.AffectedService)
		fmt.Fprintf(rec.out, "  root_cause       : %s\n", d.RootCause)
		fmt.Fprintf(rec.out, "  recommendation   : %s\n", d.Recommendation)
		fmt.Fprintf(rec.out, "  evidence_summary : %s\n", d.EvidenceSummary)
		fmt.Fprintf(rec.out, "========================================\n\n")
	} else {
		// Fallback: pretty-print unknown payload
		var pretty interface{}
		if json.Unmarshal(body, &pretty) == nil {
			out, _ := json.MarshalIndent(pretty, "", "  ")
			fmt.Fprintf(rec.out, "[%s] KAPE payload (unknown shape):\n%s\n---\n", ts, out)
		} else {
			fmt.Fprintf(rec.out, "[%s] KAPE payload (raw): %s\n---\n", ts, body)
		}
	}

	w.WriteHeader(http.StatusOK)
}
