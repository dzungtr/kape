package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// DaemonClient is the controller-side view of the in-sandbox daemon
// (issue #212, ADR-0083 D3). The puller consumes Records; Prompt/Abort ride
// Command; Status is the reconnect preflight (D6, consumed by #213).
type DaemonClient interface {
	// Status returns the daemon's boot identity and pi liveness.
	Status(ctx context.Context) (DaemonStatus, error)
	// Records long-polls the spool for records with spool_id > after;
	// wait bounds the server-side block when no records are available.
	Records(ctx context.Context, after uint64, wait time.Duration) ([]DaemonRecord, error)
	// Command posts one command; acceptance is 202 (D4) — settlement flows
	// via the records stream.
	Command(ctx context.Context, c DaemonCommand) (DaemonCommand, error)
}

// DaemonStatus is the GET /status body.
type DaemonStatus struct {
	AgentID   string `json:"agent_id"`
	BootID    string `json:"boot_id"`
	PiRunning bool   `json:"pi_running"`
}

// DaemonRecord is one `{spool_id, data}` spool envelope: data is the
// verbatim pi record (never re-wrapped).
type DaemonRecord struct {
	SpoolID uint64          `json:"spool_id"`
	Data    json.RawMessage `json:"data"`
}

// DaemonCommand is the POST /commands body (and response).
type DaemonCommand struct {
	ID      string          `json:"command_id"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Status  string          `json:"status,omitempty"`
}

// HTTPDaemonClient talks the D3 REST surface: GET /status, GET
// /records?after=&wait=, POST /commands. Auth is the D5 placeholder static
// bearer token; 401 (missing/wrong credential) surfaces as an error.
type HTTPDaemonClient struct {
	base  string // e.g. "http://127.0.0.1:9090"
	token string
	hc    *http.Client
}

// NewHTTPDaemonClient builds the client. The http.Client carries no overall
// timeout — long-poll calls get their own per-request deadline (below).
func NewHTTPDaemonClient(base, token string) *HTTPDaemonClient {
	return &HTTPDaemonClient{base: base, token: token, hc: &http.Client{}}
}

// do issues one authenticated request and decodes the JSON response; a
// non-2xx status becomes an error naming the endpoint and status.
func (c *HTTPDaemonClient) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %s body: %w", path, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("daemon %s %s: %s: %s", method, path, resp.Status, string(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode %s response: %w", path, err)
		}
	}
	return nil
}

func (c *HTTPDaemonClient) Status(ctx context.Context) (DaemonStatus, error) {
	var st DaemonStatus
	err := c.do(ctx, http.MethodGet, "/status", nil, &st)
	return st, err
}

func (c *HTTPDaemonClient) Records(ctx context.Context, after uint64, wait time.Duration) ([]DaemonRecord, error) {
	// The server blocks up to wait; bound the request a little past it so a
	// hung daemon cannot stall the puller forever.
	rctx := ctx
	if wait > 0 {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, wait+10*time.Second)
		defer cancel()
	}
	var out struct {
		Records []DaemonRecord `json:"records"`
	}
	path := "/records?after=" + strconv.FormatUint(after, 10)
	if wait > 0 {
		path += "&wait=" + wait.String()
	}
	if err := c.do(rctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Records, nil
}

func (c *HTTPDaemonClient) Command(ctx context.Context, cmd DaemonCommand) (DaemonCommand, error) {
	var out DaemonCommand
	err := c.do(ctx, http.MethodPost, "/commands", cmd, &out)
	return out, err
}
