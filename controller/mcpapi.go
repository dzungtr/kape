package main

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCP transport (spec #172, issue #178): the eight-tool contract exposed as
// MCP tools on Streamable HTTP, same port and process as REST. Every tool is
// a thin wrapper over the shared AgentManager/EventHub core — one tool
// implementation per verb, two transports (REST in http.go, MCP here).
//

// SDK decision (spec #172 UNVERIFIED, resolved): the official
// github.com/modelcontextprotocol/go-sdk (v1.8.0, stable 1.x line) is used.
// It provides Streamable HTTP server + client transports, schema generation
// from Go structs, and typed tool handlers — mark3labs/mcp-go was not
// needed. See README.md for the full rationale.

// RestVerbs is the REST/tool contract verb list — the authoritative names
// for the contract-equality test and the source of the MCP tool names.
func RestVerbs() []string {
	return []string{
		"create_agent",
		"get_agent",
		"prompt_agent",
		"abort",
		"stream_events",
		"read_events",
		"delete_agent",
		"list_agents",
	}
}

// NewMCPServer builds the MCP server exposing the tool contract. Tools map
// 1:1 onto the REST verbs in RestVerbs(); semantics are identical — same
// handler core, same error meanings (validation, unknown agent, turn in
// flight), surfaced as MCP tool errors instead of HTTP status codes.
func NewMCPServer(mgr *AgentManager) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "kape-controller", Version: "v1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_agent",
		Description: "Create a sandbox agent running the pi coding agent. All profile fields optional; defaults from controller config. Returns the agent view {id, sandbox, status}.",
	}, handleCreateAgent(mgr))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_agent",
		Description: "Get one sandbox agent's detail incl. current status. Errors if the agent id is unknown.",
	}, handleGetAgent(mgr))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "prompt_agent",
		Description: "Send a prompt (work item) to an agent. Returns once the turn is accepted; a tool error is returned when a turn is already in flight (409-equivalent) or the agent cannot accept work.",
	}, handlePromptAgent(mgr))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "abort",
		Description: "Abort the agent's in-flight turn. The stream stays open; the turn settles.",
	}, handleAbort(mgr))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "stream_events",
		Description: "Subscribe to an agent's event stream from a cursor. Over MCP this returns the buffered replay from the cursor as one batch (tool results cannot stream); for live push use the REST SSE endpoint GET /agents/{id}/events with Accept: text/event-stream (re-attach via Last-Event-ID or after).",
	}, handleStreamEvents(mgr))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "read_events",
		Description: "Pull a bounded batch of an agent's events by cursor (after+limit+types), no gaps and no duplicates across polls. text_delta records are excluded by default; pass include_deltas=true to receive them.",
	}, handleReadEvents(mgr))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_agent",
		Description: "Abort the agent's exec stream and delete its sandbox. The agent is terminated; subsequent calls error as unknown.",
	}, handleDeleteAgent(mgr))
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_agents",
		Description: "List the sandbox agents owned by this controller: gateway sandboxes carrying the managed-by ownership label, joined with in-memory live state. Includes post-restart CR-derived views.",
	}, handleListAgents(mgr))
	return srv
}

// --- tool input/output shapes ---------------------------------------------
//
// Input structs mirror the REST contract field-for-field (required fields
// have no omitempty; optional ones do, so generated schemas mark them
// optional). Output shapes reuse the REST response structs.

type agentIDInput struct {
	AgentID string `json:"agent_id"`
}

type promptInput struct {
	AgentID string `json:"agent_id"`
	Message string `json:"message"`
}

type readEventsInput struct {
	AgentID       string   `json:"agent_id"`
	After         uint64   `json:"after,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	Types         []string `json:"types,omitempty"`
	IncludeDeltas bool     `json:"include_deltas,omitempty"`
}

type streamEventsInput struct {
	AgentID string `json:"agent_id"`
	After   uint64 `json:"after,omitempty"`
}

// --- tool handlers ---------------------------------------------------------

func handleCreateAgent(mgr *AgentManager) mcp.ToolHandlerFor[CreateProfile, AgentView] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input CreateProfile) (*mcp.CallToolResult, AgentView, error) {
		agent, err := mgr.Create(ctx, &input)
		if err != nil {
			return nil, AgentView{}, err // validation errors name the field
		}
		return nil, agent.agentView(), nil
	}
}

// handleGetAgent routes through GetView so MCP gets the same post-restart
// CR-derived parity as the REST GET twin.
func handleGetAgent(mgr *AgentManager) mcp.ToolHandlerFor[agentIDInput, AgentView] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input agentIDInput) (*mcp.CallToolResult, AgentView, error) {
		view, err := mgr.GetView(ctx, input.AgentID)
		if err != nil {
			return nil, AgentView{}, err
		}
		return nil, view, nil
	}
}

func handleListAgents(mgr *AgentManager) mcp.ToolHandlerFor[struct{}, []AgentView] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, []AgentView, error) {
		views, err := mgr.List(ctx)
		if err != nil {
			return nil, nil, err
		}
		if views == nil {
			views = []AgentView{}
		}
		return nil, views, nil
	}
}

func handlePromptAgent(mgr *AgentManager) mcp.ToolHandlerFor[promptInput, map[string]string] {
	return func(_ context.Context, _ *mcp.CallToolRequest, input promptInput) (*mcp.CallToolResult, map[string]string, error) {
		if input.Message == "" {
			return nil, nil, errors.New("message must be a non-empty string")
		}
		if mgr.Get(input.AgentID) == nil {
			return nil, nil, errUnknownAgent(input.AgentID)
		}
		if err := mgr.Prompt(input.AgentID, input.Message); err != nil {
			return nil, nil, err
		}
		return nil, map[string]string{"status": "accepted"}, nil
	}
}

func handleAbort(mgr *AgentManager) mcp.ToolHandlerFor[agentIDInput, map[string]string] {
	return func(_ context.Context, _ *mcp.CallToolRequest, input agentIDInput) (*mcp.CallToolResult, map[string]string, error) {
		if mgr.Get(input.AgentID) == nil {
			return nil, nil, errUnknownAgent(input.AgentID)
		}
		if err := mgr.Abort(input.AgentID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]string{"status": "accepted"}, nil
	}
}

// handleStreamEvents returns the buffered replay from the cursor. Live push
// is a REST SSE capability (see the tool description).
func handleStreamEvents(mgr *AgentManager) mcp.ToolHandlerFor[streamEventsInput, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, input streamEventsInput) (*mcp.CallToolResult, any, error) {
		agent := mgr.Get(input.AgentID)
		if agent == nil {
			return nil, readEventsResponse{}, errUnknownAgent(input.AgentID)
		}
		replay, _, unsub := agent.hub.Subscribe(input.After)
		defer unsub()
		evs := make([]Event, len(replay))
		copy(evs, replay)
		return nil, readEventsResponse{AgentID: agent.ID, Events: evs, Truncated: agent.hub.Truncated()}, nil
	}
}

// handleReadEvents is the MCP read_events tool: cursor+limit+types, with the
// non-delta default (include_deltas=false excludes text_delta records).
func handleReadEvents(mgr *AgentManager) mcp.ToolHandlerFor[readEventsInput, any] {
	return func(_ context.Context, _ *mcp.CallToolRequest, input readEventsInput) (*mcp.CallToolResult, any, error) {
		agent := mgr.Get(input.AgentID)
		if agent == nil {
			return nil, readEventsResponse{}, errUnknownAgent(input.AgentID)
		}
		limit := input.Limit
		if limit == 0 {
			limit = defaultReadLimit
		}
		if limit < 0 {
			return nil, readEventsResponse{}, errors.New("limit must be a positive integer")
		}
		var exclude []string
		if !input.IncludeDeltas {
			exclude = []string{textDeltaEventType}
		}
		res := agent.hub.ReadFiltered(input.After, limit, input.Types, exclude)
		if res.Events == nil {
			res.Events = []Event{}
		}
		return nil, readEventsResponse{AgentID: agent.ID, Events: res.Events, Truncated: res.Truncated}, nil
	}
}

func handleDeleteAgent(mgr *AgentManager) mcp.ToolHandlerFor[agentIDInput, map[string]string] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, input agentIDInput) (*mcp.CallToolResult, map[string]string, error) {
		if err := mgr.Delete(ctx, input.AgentID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]string{"status": "terminated"}, nil
	}
}
