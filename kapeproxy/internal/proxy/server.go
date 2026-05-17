package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// MCP standard error codes used by kapeproxy.
const (
	MCPErrMethodNotFound = -32601 // "method not found" — used for unknown / disallowed tools per spec
	MCPErrServerError    = -32000 // "server error" — used when upstream unavailable
)

// Server fronts the kapeproxy MCP endpoint on :8080.
type Server struct {
	router   *Router
	redactor *Redactor
	audit    *AuditLogger
	logger   zerolog.Logger

	httpServer *http.Server
}

// NewServer wires the dependencies. The HTTP server is built but not yet
// listening; call Start.
func NewServer(addr string, r *Router, red *Redactor, a *AuditLogger, logger zerolog.Logger) *Server {
	s := &Server{router: r, redactor: red, audit: a, logger: logger}
	mux := http.NewServeMux()
	mux.Handle("/", s.mcpHandler())
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Start listens on the configured address. Blocks until the listener errors
// (call Shutdown from another goroutine to stop).
func (s *Server) Start() error {
	s.logger.Info().Str("addr", s.httpServer.Addr).Msg("kapeproxy listening")
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Shutdown drains in-flight requests then closes upstream connections.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	for name, up := range s.router.Upstreams() {
		if err := up.Close(); err != nil {
			s.logger.Warn().Str("upstream", name).Err(err).Msg("upstream close error")
		}
	}
	return nil
}

// jsonrpcRequest is the JSON-RPC 2.0 request envelope.
type jsonrpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

// jsonrpcResponse is the JSON-RPC 2.0 response envelope.
type jsonrpcResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  any            `json:"result,omitempty"`
	Error   *jsonrpcError  `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpHandler returns an http.Handler that speaks MCP over HTTP JSON-RPC.
func (s *Server) mcpHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		taskID := r.Header.Get("X-Kape-Task-Id")

		var req jsonrpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONRPCError(w, nil, MCPErrServerError, "invalid JSON-RPC request")
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch req.Method {
		case "tools/list":
			names := s.handleToolsList(ctx, taskID)
			tools := make([]map[string]string, 0, len(names))
			for _, n := range names {
				tools = append(tools, map[string]string{"name": n})
			}
			writeJSONRPCResult(w, req.ID, map[string]any{"tools": tools})

		case "tools/call":
			name, _ := req.Params["name"].(string)
			args, _ := req.Params["arguments"].(map[string]any)
			result, errCode, callErr := s.handleToolsCall(ctx, name, args, taskID)
			if callErr != nil {
				writeJSONRPCError(w, req.ID, errCode, callErr.Error())
				return
			}
			writeJSONRPCResult(w, req.ID, result)

		default:
			writeJSONRPCError(w, req.ID, MCPErrMethodNotFound, fmt.Sprintf("unknown method %q", req.Method))
		}
	})
}

func writeJSONRPCResult(w http.ResponseWriter, id any, result any) {
	_ = json.NewEncoder(w).Encode(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func writeJSONRPCError(w http.ResponseWriter, id any, code int, msg string) {
	_ = json.NewEncoder(w).Encode(jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: msg},
	})
}

// handleToolsList returns the router's full tool list.
func (s *Server) handleToolsList(ctx context.Context, taskID string) []string {
	_ = ctx
	_ = taskID
	return s.router.List()
}

// handleToolsCall dispatches one call: route → redact-input → upstream-call → redact-output.
// Returns (result, mcpErrorCode, error). When mcpErrorCode != 0, error is the message.
func (s *Server) handleToolsCall(ctx context.Context, namespaced string, args map[string]any, taskID string) (any, int, error) {
	start := time.Now()
	entry, ok := s.router.Route(namespaced)
	if !ok {
		_, span := StartCallSpan(ctx, CallAttrs{NamespacedName: namespaced, Allowed: false, TaskID: taskID})
		FinishCallSpan(span, 0, fmt.Errorf("disallowed tool %q", namespaced))
		s.audit.LogIfEnabled(true, AuditEntry{
			NamespacedName: namespaced, Allowed: false,
			Error:  "disallowed or unknown tool",
			TaskID: taskID,
		})
		return nil, MCPErrMethodNotFound, fmt.Errorf("tool %q not allowed", namespaced)
	}

	ctx, span := StartCallSpan(ctx, CallAttrs{
		NamespacedName: namespaced,
		Upstream:       entry.Upstream.Name(),
		OriginalName:   entry.OriginalName,
		Allowed:        true,
		TaskID:         taskID,
	})

	if !entry.Upstream.Available() {
		err := fmt.Errorf("upstream %q unavailable", entry.Upstream.Name())
		FinishCallSpan(span, time.Since(start).Milliseconds(), err)
		s.audit.LogIfEnabled(entry.Audit, AuditEntry{
			NamespacedName: namespaced, Upstream: entry.Upstream.Name(),
			OriginalName: entry.OriginalName, Allowed: true,
			LatencyMS: time.Since(start).Milliseconds(),
			Error:     err.Error(), TaskID: taskID,
		})
		return nil, MCPErrServerError, err
	}

	redArgs := args
	if entry.Redaction != nil && len(entry.Redaction.Input) > 0 {
		out, err := s.redactor.Apply(args, entry.Redaction.Input)
		if err != nil {
			FinishCallSpan(span, time.Since(start).Milliseconds(), err)
			return nil, MCPErrServerError, fmt.Errorf("input redaction: %w", err)
		}
		redArgs = out.(map[string]any)
	}

	mu, ok := entry.Upstream.(interface {
		CallToolCtx(context.Context, string, map[string]any) (any, error)
	})
	if !ok {
		err := fmt.Errorf("upstream %q does not support context-aware CallToolCtx", entry.Upstream.Name())
		FinishCallSpan(span, time.Since(start).Milliseconds(), err)
		return nil, MCPErrServerError, err
	}
	res, err := mu.CallToolCtx(ctx, entry.OriginalName, redArgs)
	if err != nil {
		FinishCallSpan(span, time.Since(start).Milliseconds(), err)
		s.audit.LogIfEnabled(entry.Audit, AuditEntry{
			NamespacedName: namespaced, Upstream: entry.Upstream.Name(),
			OriginalName: entry.OriginalName, Allowed: true,
			LatencyMS: time.Since(start).Milliseconds(),
			Error:     err.Error(), TaskID: taskID,
		})
		return nil, MCPErrServerError, err
	}

	if entry.Redaction != nil && len(entry.Redaction.Output) > 0 {
		out, err := s.redactor.Apply(res, entry.Redaction.Output)
		if err != nil {
			FinishCallSpan(span, time.Since(start).Milliseconds(), err)
			return nil, MCPErrServerError, fmt.Errorf("output redaction: %w", err)
		}
		res = out
	}

	FinishCallSpan(span, time.Since(start).Milliseconds(), nil)
	s.audit.LogIfEnabled(entry.Audit, AuditEntry{
		NamespacedName: namespaced, Upstream: entry.Upstream.Name(),
		OriginalName: entry.OriginalName, Allowed: true,
		LatencyMS: time.Since(start).Milliseconds(),
		TaskID:    taskID,
	})
	return res, 0, nil
}
