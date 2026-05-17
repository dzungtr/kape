package proxy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// mcpUpstream is the production Upstream implementation backed by the MCP Go SDK.
//
// Construction (NewMCPUpstream) attempts to dial + handshake; failure is non-fatal
// (the upstream is returned with available=false; callers see MCP errors at call-time).
type mcpUpstream struct {
	name      string
	url       string
	transport string

	mu        sync.RWMutex
	available bool
	tools     []string // names cached after handshake
	session   *mcp.ClientSession
	client    *mcp.Client
}

// NewMCPUpstream dials the upstream over the configured transport.
// Connection failure is logged and returns an Upstream with Available()=false;
// it never returns an error (per spec D2: unreachable-at-startup is non-fatal).
func NewMCPUpstream(ctx context.Context, name string, cfg *UpstreamConfig) Upstream {
	u := &mcpUpstream{
		name:      name,
		url:       cfg.URL,
		transport: cfg.Transport,
	}

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	transport, err := newTransport(cfg.Transport, cfg.URL)
	if err != nil {
		log.Warn().
			Str("upstream", name).
			Str("url", cfg.URL).
			Err(err).
			Msg("upstream transport error; will return MCP error on call")
		return u
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "kapeproxy", Version: "1.0.0"}, nil)
	session, err := client.Connect(dialCtx, transport, nil)
	if err != nil {
		log.Warn().
			Str("upstream", name).
			Str("url", cfg.URL).
			Err(err).
			Msg("upstream unreachable at startup; will return MCP error on call")
		return u
	}

	result, err := session.ListTools(dialCtx, nil)
	if err != nil {
		log.Warn().Str("upstream", name).Err(err).Msg("ListTools failed at startup; marking unavailable")
		_ = session.Close()
		return u
	}

	tools := make([]string, 0, len(result.Tools))
	for _, t := range result.Tools {
		tools = append(tools, t.Name)
	}

	u.client = client
	u.session = session
	u.tools = tools
	u.available = true
	return u
}

func (u *mcpUpstream) Name() string { return u.name }

func (u *mcpUpstream) Available() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.available
}

func (u *mcpUpstream) ListTools() []string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	out := make([]string, len(u.tools))
	copy(out, u.tools)
	return out
}

// CallTool satisfies the Upstream interface. Server.handleToolsCall uses
// CallToolCtx which carries a real context with trace propagation.
func (u *mcpUpstream) CallTool(_ string, tool string, args map[string]any) (any, error) {
	return u.CallToolCtx(context.Background(), tool, args)
}

// CallToolCtx is the production entry point used by Server.handleToolsCall.
// It injects W3C TraceContext into the outbound request via OTEL propagators.
func (u *mcpUpstream) CallToolCtx(ctx context.Context, tool string, args map[string]any) (any, error) {
	u.mu.RLock()
	avail := u.available
	session := u.session
	u.mu.RUnlock()

	if !avail || session == nil {
		return nil, fmt.Errorf("upstream %q unavailable", u.name)
	}

	// Outbound TraceContext propagation.
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      tool,
		Arguments: args,
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (u *mcpUpstream) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.available = false
	if u.session == nil {
		return nil
	}
	return u.session.Close()
}

// newTransport creates an MCP transport for the given transport type and URL.
func newTransport(transportType, url string) (mcp.Transport, error) {
	switch transportType {
	case "sse":
		return &mcp.SSEClientTransport{Endpoint: url}, nil
	case "streamable-http":
		return &mcp.StreamableClientTransport{
			Endpoint:             url,
			DisableStandaloneSSE: true,
		}, nil
	default:
		return nil, fmt.Errorf("unknown transport %q (expected sse|streamable-http)", transportType)
	}
}
